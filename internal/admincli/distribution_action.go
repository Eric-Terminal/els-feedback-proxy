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
	"strings"
)

func runDistributionAction(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		writeDistributionActionHelp(stdout)
		return nil
	}
	var err error
	switch args[0] {
	case "list":
		err = runDistributionActionList(args[1:], stdout, stderr)
	case "upload":
		err = runDistributionActionUpload(args[1:], stdout, stderr)
	case "update":
		err = runDistributionActionUpdate(args[1:], stdout, stderr)
	case "delete":
		err = runDistributionActionDelete(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("未知官方数据库操作命令 %q；使用 distribution action --help 查看用法", args[0])
	}
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func runDistributionActionList(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("distribution action list", stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy distribution action list [--admin-url URL]")
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	client, err := newAdminClient(*adminURL)
	if err != nil {
		return err
	}
	return client.request(http.MethodGet, "/v1/admin/distribution/actions", nil, stdout)
}

func runDistributionActionUpload(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("distribution action upload", stderr)
	filePath := flags.String("file", "", "要上传的官方操作配方 JSON")
	disabled := flags.Bool("disabled", false, "上传后暂不进入公开清单")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy distribution action upload --file 操作配方.json [--disabled]")
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if strings.TrimSpace(*filePath) == "" {
		return errors.New("必须提供 --file")
	}
	client, err := newAdminClient(*adminURL)
	if err != nil {
		return err
	}
	return client.requestDistributionActionMultipart(
		http.MethodPost,
		"/v1/admin/distribution/actions",
		!*disabled,
		*filePath,
		stdout,
	)
}

func runDistributionActionUpdate(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("distribution action update", stderr)
	key := flags.String("key", "", "要更新的官方数据库操作 key")
	filePath := flags.String("file", "", "可选的替换操作配方 JSON")
	disabled := flags.Bool("disabled", false, "从公开清单停用")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy distribution action update --key KEY [--file 操作配方.json] [--disabled]")
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
	return client.requestDistributionActionMultipart(
		http.MethodPut,
		"/v1/admin/distribution/actions/"+url.PathEscape(*key),
		!*disabled,
		*filePath,
		stdout,
	)
}

func runDistributionActionDelete(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("distribution action delete", stderr)
	key := flags.String("key", "", "要删除的官方数据库操作 key")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy distribution action delete --key KEY [--admin-url URL]")
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
		"/v1/admin/distribution/actions/"+url.PathEscape(*key),
		nil,
		io.Discard,
	); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{"success": true, "key": *key})
}

func (client *adminClient) requestDistributionActionMultipart(
	method string,
	requestPath string,
	enabled bool,
	filePath string,
	stdout io.Writer,
) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
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
		return fmt.Errorf("创建官方数据库操作请求失败: %w", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return client.perform(request, stdout)
}

func writeDistributionActionHelp(writer io.Writer) {
	fmt.Fprintln(writer, `官方数据库操作管理命令

用法:
  els-feedback-proxy distribution action list
  els-feedback-proxy distribution action upload --file 操作配方.json
  els-feedback-proxy distribution action update --key KEY [--file 操作配方.json]
  els-feedback-proxy distribution action delete --key KEY

upload 和 update 可使用 --disabled 停止公开下发。操作配方会由服务端校验后进入公开清单；修改同一操作内容时必须递增 revision。`)
}
