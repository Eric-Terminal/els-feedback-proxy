package admincli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultAdminURL    = "http://127.0.0.1:8521"
	maxCLIRequestBody  = 64 << 10
	maxCLIResponseBody = 1 << 20
)

type adminClient struct {
	baseURL string
	token   string
	http    *http.Client
}

// Run 识别并执行管理命令；没有 CLI 参数时交回服务端启动流程。
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}

	switch args[0] {
	case "announcement", "announcements":
		return true, runAnnouncement(args[1:], stdin, stdout, stderr)
	case "distribution":
		return true, runDistribution(args[1:], stdout, stderr)
	case "survey", "surveys":
		return true, runSurvey(args[1:], stdin, stdout, stderr)
	case "help", "--help", "-h":
		writeRootHelp(stdout)
		return true, nil
	default:
		return true, fmt.Errorf("未知命令 %q；使用 --help 查看可用命令", args[0])
	}
}

func runAnnouncement(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		writeAnnouncementHelp(stdout)
		return nil
	}

	var err error
	switch args[0] {
	case "list":
		err = runAnnouncementList(args[1:], stdout, stderr)
	case "create":
		err = runAnnouncementCreate(args[1:], stdin, stdout, stderr)
	case "update":
		err = runAnnouncementUpdate(args[1:], stdin, stdout, stderr)
	case "delete":
		err = runAnnouncementDelete(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("未知公告命令 %q；使用 announcement --help 查看用法", args[0])
	}
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func runAnnouncementList(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("announcement list", stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy announcement list [--admin-url URL]")
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}

	client, err := newAdminClient(*adminURL)
	if err != nil {
		return err
	}
	return client.request(http.MethodGet, "/v1/admin/announcements", nil, stdout)
}

func runAnnouncementCreate(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) error {
	flags, adminURL := newCommandFlagSet("announcement write", stderr)
	file := flags.String("file", "", "公告 JSON 文件路径；使用 - 从标准输入读取")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy announcement create --file <路径|-> [--admin-url URL]")
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if strings.TrimSpace(*file) == "" {
		return errors.New("必须提供 --file")
	}

	body, err := readRequestBody(*file, stdin)
	if err != nil {
		return err
	}
	client, err := newAdminClient(*adminURL)
	if err != nil {
		return err
	}

	return client.request(http.MethodPost, "/v1/admin/announcements", body, stdout)
}

func runAnnouncementUpdate(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("announcement update", stderr)
	key := flags.String("key", "", "要更新的公告 key")
	file := flags.String("file", "", "公告 JSON 文件路径；使用 - 从标准输入读取")
	flags.Usage = func() {
		fmt.Fprintln(
			stderr,
			"用法: els-feedback-proxy announcement update --key KEY --file <路径|-> [--admin-url URL]",
		)
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if strings.TrimSpace(*key) == "" {
		return errors.New("必须提供 --key")
	}
	if strings.TrimSpace(*file) == "" {
		return errors.New("必须提供 --file")
	}

	body, err := readRequestBody(*file, stdin)
	if err != nil {
		return err
	}
	client, err := newAdminClient(*adminURL)
	if err != nil {
		return err
	}
	return client.request(
		http.MethodPut,
		"/v1/admin/announcements/"+url.PathEscape(*key),
		body,
		stdout,
	)
}

func runAnnouncementDelete(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("announcement delete", stderr)
	key := flags.String("key", "", "要删除的公告 key")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy announcement delete --key KEY [--admin-url URL]")
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if strings.TrimSpace(*key) == "" {
		return errors.New("必须提供 --key")
	}

	client, err := newAdminClient(*adminURL)
	if err != nil {
		return err
	}
	if err := client.request(
		http.MethodDelete,
		"/v1/admin/announcements/"+url.PathEscape(*key),
		nil,
		io.Discard,
	); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{"success": true, "key": *key})
}

func newCommandFlagSet(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	adminURL := flags.String(
		"admin-url",
		getEnv("ELS_ADMIN_URL", defaultAdminURL),
		"管理 API 地址",
	)
	return flags, adminURL
}

func parseCommandFlags(flags *flag.FlagSet, args []string) error {
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("存在无法识别的参数: %s", strings.Join(flags.Args(), " "))
	}
	return nil
}

func newAdminClient(rawURL string) (*adminClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("管理 API 地址无效")
	}

	token := strings.TrimSpace(os.Getenv("ANNOUNCEMENT_ADMIN_TOKEN"))
	if token == "" {
		return nil, errors.New("缺少环境变量 ANNOUNCEMENT_ADMIN_TOKEN")
	}
	return &adminClient{
		baseURL: strings.TrimRight(parsed.String(), "/"),
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (client *adminClient) request(
	method string,
	path string,
	body []byte,
	stdout io.Writer,
) error {
	request, err := http.NewRequest(method, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建管理请求失败: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return client.perform(request, stdout)
}

func (client *adminClient) perform(request *http.Request, stdout io.Writer) error {
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return fmt.Errorf("连接管理 API 失败: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxCLIResponseBody+1))
	if err != nil {
		return fmt.Errorf("读取管理 API 响应失败: %w", err)
	}
	if len(responseBody) > maxCLIResponseBody {
		return errors.New("管理 API 响应超过大小限制")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(responseBody))
		if message == "" {
			message = response.Status
		}
		return fmt.Errorf("管理 API 返回 %d: %s", response.StatusCode, message)
	}
	if len(responseBody) == 0 || stdout == io.Discard {
		return nil
	}

	var payload any
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return fmt.Errorf("管理 API 返回了无效 JSON: %w", err)
	}
	return writeJSON(stdout, payload)
}

func readRequestBody(path string, stdin io.Reader) ([]byte, error) {
	var reader io.Reader
	if path == "-" {
		reader = stdin
	} else {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("打开 JSON 文件失败: %w", err)
		}
		defer file.Close()
		reader = file
	}

	body, err := io.ReadAll(io.LimitReader(reader, maxCLIRequestBody+1))
	if err != nil {
		return nil, fmt.Errorf("读取 JSON 失败: %w", err)
	}
	if len(body) > maxCLIRequestBody {
		return nil, errors.New("JSON 超过 64 KiB")
	}
	if !json.Valid(body) {
		return nil, errors.New("文件不是有效 JSON")
	}
	return body, nil
}

func writeJSON(writer io.Writer, payload any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return fmt.Errorf("输出 JSON 失败: %w", err)
	}
	return nil
}

func writeRootHelp(writer io.Writer) {
	fmt.Fprintln(writer, `ELS Feedback Proxy

用法:
  els-feedback-proxy                         启动服务
  els-feedback-proxy announcement <命令>    通过管理 API 操作公告
  els-feedback-proxy survey <命令>          通过管理 API 操作意见征集
  els-feedback-proxy distribution <命令>    通过管理 API 操作官方数据

使用对应命令的 --help 查看详细用法。`)
}

func writeAnnouncementHelp(writer io.Writer) {
	fmt.Fprintln(writer, `公告管理命令

用法:
  els-feedback-proxy announcement list
  els-feedback-proxy announcement create --file <路径|->
  els-feedback-proxy announcement update --key KEY --file <路径|->
  els-feedback-proxy announcement delete --key KEY

环境变量:
  ANNOUNCEMENT_ADMIN_TOKEN  管理口令（必填）
  ELS_ADMIN_URL             管理 API 地址（默认 http://127.0.0.1:8521）

每个子命令也可以通过 --admin-url 临时指定管理 API 地址。`)
}

func getEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value != "" {
		return value
	}
	return fallback
}
