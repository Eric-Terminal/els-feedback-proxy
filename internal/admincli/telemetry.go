package admincli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func runTelemetry(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		writeTelemetryHelp(stdout)
		return nil
	}

	var err error
	switch args[0] {
	case "status":
		err = runTelemetryStatus(args[1:], stdout, stderr)
	case "manifest":
		err = runTelemetryManifest(args[1:], stdout, stderr)
	case "export":
		err = runTelemetryExport(args[1:], stdout, stderr)
	case "confirm":
		err = runTelemetryConfirm(args[1:], stdin, stdout, stderr)
	default:
		return fmt.Errorf("未知遥测命令 %q；使用 telemetry --help 查看用法", args[0])
	}
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func runTelemetryStatus(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("telemetry status", stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy telemetry status [--admin-url URL]")
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	client, err := newAdminClient(*adminURL)
	if err != nil {
		return err
	}
	return client.request(http.MethodGet, "/v1/admin/telemetry/status", nil, stdout)
}

func runTelemetryManifest(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("telemetry manifest", stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy telemetry manifest [--admin-url URL]")
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	client, err := newAdminClient(*adminURL)
	if err != nil {
		return err
	}
	return client.request(http.MethodGet, "/v1/admin/telemetry/manifest", nil, stdout)
}

func runTelemetryExport(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("telemetry export", stderr)
	payloadID := flags.String("payload-id", "", "要导出的遥测 payload_id")
	flags.Usage = func() {
		fmt.Fprintln(
			stderr,
			"用法: els-feedback-proxy telemetry export --payload-id SHA256 [--admin-url URL]",
		)
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if strings.TrimSpace(*payloadID) == "" {
		return errors.New("必须提供 --payload-id")
	}
	client, err := newAdminClient(*adminURL)
	if err != nil {
		return err
	}
	return client.requestRaw(
		"/v1/admin/telemetry/files/"+url.PathEscape(strings.TrimSpace(*payloadID)),
		stdout,
	)
}

func runTelemetryConfirm(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) error {
	flags, adminURL := newCommandFlagSet("telemetry confirm", stderr)
	file := flags.String("file", "", "确认 JSON 文件路径；使用 - 从标准输入读取")
	flags.Usage = func() {
		fmt.Fprintln(
			stderr,
			"用法: els-feedback-proxy telemetry confirm --file <路径|-> [--admin-url URL]",
		)
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
	return client.request(http.MethodPost, "/v1/admin/telemetry/confirm", body, stdout)
}

func writeTelemetryHelp(writer io.Writer) {
	fmt.Fprintln(writer, `性能遥测管理命令

用法:
  els-feedback-proxy telemetry status
  els-feedback-proxy telemetry manifest
  els-feedback-proxy telemetry export --payload-id SHA256
  els-feedback-proxy telemetry confirm --file <路径|->

confirm 的 JSON 格式为 {"payload_ids":["<SHA256>"]}。只有本地文件完成大小、
SHA-256 与 JSON 校验后才应确认；服务端收到确认后会精确删除对应原始文件。

环境变量:
  ANNOUNCEMENT_ADMIN_TOKEN  管理口令（必填）
  ELS_ADMIN_URL             管理 API 地址（默认 http://127.0.0.1:8521）`)
}
