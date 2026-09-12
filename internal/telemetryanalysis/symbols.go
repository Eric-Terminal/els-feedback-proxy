package telemetryanalysis

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var (
	uuidPattern      = regexp.MustCompile(`^[0-9A-F]{32}$`)
	dwarfUUIDPattern = regexp.MustCompile(
		`UUID:\s+([0-9A-Fa-f-]{36})\s+\(([^)]+)\)\s+(.+)$`,
	)
)

type symbolFile struct {
	UUID         string
	Architecture string
	DwarfPath    string
	DSYMPath     string
}

type symbolCatalog struct {
	mu             sync.Mutex
	entries        map[string][]symbolFile
	cache          map[string]symbolicationResult
	spotlightTried map[string]bool
	useSpotlight   bool
}

func newSymbolCatalog(paths []string, useSpotlight bool) (*symbolCatalog, error) {
	catalog := &symbolCatalog{
		entries:        make(map[string][]symbolFile),
		cache:          make(map[string]symbolicationResult),
		spotlightTried: make(map[string]bool),
		useSpotlight:   useSpotlight,
	}
	for _, path := range paths {
		if err := catalog.addPath(path); err != nil {
			return nil, fmt.Errorf("加载符号路径 %s 失败: %w", path, err)
		}
	}
	return catalog, nil
}

func (c *symbolCatalog) Symbolicate(
	uuid string,
	binaryName string,
	architecture string,
	address uint64,
	offset uint64,
) symbolicationResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	uuid = normalizeUUID(uuid)
	cacheKey := fmt.Sprintf("%s|%s|%x|%x", uuid, architecture, address, offset)
	if offset > 0 {
		// UUID 与相对偏移相同即为同一代码位置，ASLR 不应使跨报告缓存失效。
		cacheKey = fmt.Sprintf("%s|%s|offset:%x", uuid, architecture, offset)
	}
	if cached, exists := c.cache[cacheKey]; exists {
		return cached
	}
	if len(c.entries[uuid]) == 0 && c.useSpotlight && !c.spotlightTried[uuid] {
		c.spotlightTried[uuid] = true
		c.addSpotlightUUID(uuid)
	}
	symbolFile, found := c.match(uuid, architecture)
	if !found {
		result := symbolicationResult{Status: "missing_dsym"}
		c.cache[cacheKey] = result
		return result
	}

	args := []string{"atos", "-arch", symbolFile.Architecture, "-o", symbolFile.DwarfPath}
	targetAddress := address
	if address > 0 && offset > 0 && address >= offset {
		loadAddress := address - offset
		args = append(args, "-l", fmt.Sprintf("0x%x", loadAddress))
	} else if offset > 0 {
		textAddress, ok := textVMAddress(symbolFile.DwarfPath)
		if !ok {
			result := symbolicationResult{
				Status:   "missing_address",
				DSYMPath: symbolFile.DSYMPath,
			}
			c.cache[cacheKey] = result
			return result
		}
		targetAddress = textAddress + offset
	} else if address == 0 {
		result := symbolicationResult{
			Status:   "missing_address",
			DSYMPath: symbolFile.DSYMPath,
		}
		c.cache[cacheKey] = result
		return result
	}
	args = append(args, fmt.Sprintf("0x%x", targetAddress))

	output, err := exec.Command("xcrun", args...).CombinedOutput()
	symbol := strings.TrimSpace(string(output))
	if err != nil || symbol == "" || strings.HasPrefix(strings.ToLower(symbol), "0x") {
		result := symbolicationResult{
			Status:   "atos_failed",
			DSYMPath: symbolFile.DSYMPath,
		}
		c.cache[cacheKey] = result
		return result
	}
	result := symbolicationResult{
		Symbol:       symbol,
		Status:       "symbolicated",
		DSYMPath:     symbolFile.DSYMPath,
		Architecture: symbolFile.Architecture,
	}
	c.cache[cacheKey] = result
	return result
}

func (c *symbolCatalog) match(uuid, architecture string) (symbolFile, bool) {
	candidates := c.entries[uuid]
	for _, candidate := range candidates {
		if candidate.Architecture == architecture {
			return candidate, true
		}
	}
	if len(candidates) > 0 {
		return candidates[0], true
	}
	return symbolFile{}, false
}

func (c *symbolCatalog) addSpotlightUUID(uuid string) {
	if !uuidPattern.MatchString(uuid) {
		return
	}
	formatted := formatUUID(uuid)
	output, err := exec.Command(
		"mdfind",
		"com_apple_xcode_dsym_uuids == "+formatted,
	).Output()
	if err != nil {
		return
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		_ = c.addPath(strings.TrimSpace(scanner.Text()))
	}
}

func (c *symbolCatalog) addPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		if strings.Contains(path, string(filepath.Separator)+"DWARF"+string(filepath.Separator)) {
			return c.addDwarfFile(path, dsymBundlePath(path))
		}
		return nil
	}
	if strings.HasSuffix(strings.ToLower(path), ".dsym") {
		return c.addDSYM(path)
	}
	return filepath.WalkDir(path, func(
		childPath string,
		entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".dsym") {
			if err := c.addDSYM(childPath); err != nil {
				return err
			}
			return filepath.SkipDir
		}
		return nil
	})
}

func (c *symbolCatalog) addDSYM(path string) error {
	dwarfDir := filepath.Join(path, "Contents", "Resources", "DWARF")
	entries, err := os.ReadDir(dwarfDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := c.addDwarfFile(filepath.Join(dwarfDir, entry.Name()), path); err != nil {
			return err
		}
	}
	return nil
}

func (c *symbolCatalog) addDwarfFile(dwarfPath, dsymPath string) error {
	output, err := exec.Command("xcrun", "dwarfdump", "--uuid", dwarfPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("读取 dSYM UUID 失败: %s", strings.TrimSpace(string(output)))
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		matches := dwarfUUIDPattern.FindStringSubmatch(strings.TrimSpace(scanner.Text()))
		if len(matches) != 4 {
			continue
		}
		uuid := normalizeUUID(matches[1])
		c.entries[uuid] = append(c.entries[uuid], symbolFile{
			UUID:         uuid,
			Architecture: strings.TrimSpace(matches[2]),
			DwarfPath:    dwarfPath,
			DSYMPath:     dsymPath,
		})
	}
	return scanner.Err()
}

func textVMAddress(dwarfPath string) (uint64, bool) {
	output, err := exec.Command("xcrun", "otool", "-l", dwarfPath).Output()
	if err != nil {
		return 0, false
	}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	inTextSegment := false
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		if fields[0] == "segname" {
			inTextSegment = fields[1] == "__TEXT"
			continue
		}
		if inTextSegment && fields[0] == "vmaddr" {
			value := strings.TrimPrefix(strings.ToLower(fields[1]), "0x")
			address, err := strconv.ParseUint(value, 16, 64)
			return address, err == nil
		}
	}
	return 0, false
}

func normalizeUUID(raw string) string {
	replacer := strings.NewReplacer("-", "", "<", "", ">", "", " ", "")
	return strings.ToUpper(replacer.Replace(strings.TrimSpace(raw)))
}

func formatUUID(uuid string) string {
	uuid = normalizeUUID(uuid)
	if len(uuid) != 32 {
		return uuid
	}
	return strings.Join([]string{
		uuid[0:8],
		uuid[8:12],
		uuid[12:16],
		uuid[16:20],
		uuid[20:32],
	}, "-")
}

func dsymBundlePath(dwarfPath string) string {
	separator := string(filepath.Separator)
	marker := ".dSYM" + separator
	index := strings.Index(dwarfPath, marker)
	if index < 0 {
		return filepath.Dir(dwarfPath)
	}
	return dwarfPath[:index+len(".dSYM")]
}
