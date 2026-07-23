package admincli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"els-feedback-proxy/internal/store"
)

func runDistribution(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		writeDistributionHelp(stdout)
		return nil
	}

	var err error
	switch args[0] {
	case "list":
		err = runDistributionList(args[1:], stdout, stderr)
	case "upload":
		err = runDistributionUpload(args[1:], stdout, stderr)
	case "update":
		err = runDistributionUpdate(args[1:], stdout, stderr)
	case "delete":
		err = runDistributionDelete(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("未知官方数据命令 %q；使用 distribution --help 查看用法", args[0])
	}
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func runDistributionList(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("distribution list", stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy distribution list [--admin-url URL]")
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	client, err := newAdminClient(*adminURL)
	if err != nil {
		return err
	}
	return client.request(http.MethodGet, "/v1/admin/distribution", nil, stdout)
}

func runDistributionUpload(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("distribution upload", stderr)
	name := flags.String("name", "", "管理页面中的显示名称")
	destination := flags.String("path", "/Documents/Providers", "客户端 Documents 内的目标目录")
	filePath := flags.String("file", "", "要上传的本地文件")
	disabled := flags.Bool("disabled", false, "上传后暂不进入公开清单")
	flags.Usage = func() {
		fmt.Fprintln(
			stderr,
			"用法: els-feedback-proxy distribution upload --name 名称 --path /Documents/目录 --file 文件 [--disabled]",
		)
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if strings.TrimSpace(*name) == "" || strings.TrimSpace(*filePath) == "" {
		return errors.New("必须提供 --name 和 --file")
	}

	client, err := newAdminClient(*adminURL)
	if err != nil {
		return err
	}
	return client.requestDistributionMultipart(
		http.MethodPost,
		"/v1/admin/distribution",
		*name,
		*destination,
		!*disabled,
		*filePath,
		stdout,
	)
}

func runDistributionUpdate(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("distribution update", stderr)
	key := flags.String("key", "", "要更新的官方数据 key")
	name := flags.String("name", "", "管理页面中的显示名称")
	destination := flags.String("path", "/Documents/Providers", "客户端 Documents 内的目标目录")
	filePath := flags.String("file", "", "可选的替换文件")
	disabled := flags.Bool("disabled", false, "从公开清单停用")
	flags.Usage = func() {
		fmt.Fprintln(
			stderr,
			"用法: els-feedback-proxy distribution update --key KEY --name 名称 --path /Documents/目录 [--file 文件] [--disabled]",
		)
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if strings.TrimSpace(*key) == "" || strings.TrimSpace(*name) == "" {
		return errors.New("必须提供 --key 和 --name")
	}

	client, err := newAdminClient(*adminURL)
	if err != nil {
		return err
	}
	return client.requestDistributionMultipart(
		http.MethodPut,
		"/v1/admin/distribution/"+url.PathEscape(*key),
		*name,
		*destination,
		!*disabled,
		*filePath,
		stdout,
	)
}

func runDistributionDelete(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("distribution delete", stderr)
	key := flags.String("key", "", "要删除的官方数据 key")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy distribution delete --key KEY [--admin-url URL]")
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
		"/v1/admin/distribution/"+url.PathEscape(*key),
		nil,
		io.Discard,
	); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{"success": true, "key": *key})
}

func (client *adminClient) requestDistributionMultipart(
	method string,
	requestPath string,
	name string,
	destination string,
	enabled bool,
	filePath string,
	stdout io.Writer,
) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("name", name); err != nil {
		return fmt.Errorf("写入显示名称失败: %w", err)
	}
	if err := writer.WriteField("destination_path", destination); err != nil {
		return fmt.Errorf("写入目标目录失败: %w", err)
	}
	if err := writer.WriteField("enabled", fmt.Sprintf("%t", enabled)); err != nil {
		return fmt.Errorf("写入发布状态失败: %w", err)
	}
	if strings.TrimSpace(filePath) != "" {
		if err := appendDistributionFile(writer, filePath); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("结束上传表单失败: %w", err)
	}

	request, err := http.NewRequest(method, client.baseURL+requestPath, &body)
	if err != nil {
		return fmt.Errorf("创建官方数据请求失败: %w", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return client.perform(request, stdout)
}

func appendDistributionFile(writer *multipart.Writer, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("打开上传文件失败: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("读取上传文件信息失败: %w", err)
	}
	if info.Size() <= 0 {
		return errors.New("上传文件不能为空")
	}
	if info.Size() > store.MaxDistributionFileSize {
		return errors.New("上传文件不能超过 32 MiB")
	}

	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("创建上传文件字段失败: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("读取上传文件失败: %w", err)
	}
	return nil
}

func writeDistributionHelp(writer io.Writer) {
	fmt.Fprintln(writer, `官方数据管理命令

用法:
  els-feedback-proxy distribution list
  els-feedback-proxy distribution upload --name 名称 --path /Documents/目录 --file 文件
  els-feedback-proxy distribution update --key KEY --name 名称 --path /Documents/目录 [--file 文件]
  els-feedback-proxy distribution delete --key KEY

upload 和 update 可使用 --disabled 停止公开下发。
环境变量与 --admin-url 用法和 announcement 命令相同。`)
}
