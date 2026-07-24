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

func runSurvey(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		writeSurveyHelp(stdout)
		return nil
	}

	var err error
	switch args[0] {
	case "list":
		err = runSurveyList(args[1:], stdout, stderr)
	case "create":
		err = runSurveyCreate(args[1:], stdin, stdout, stderr)
	case "update":
		err = runSurveyUpdate(args[1:], stdin, stdout, stderr)
	case "delete":
		err = runSurveyDelete(args[1:], stdout, stderr)
	case "results":
		err = runSurveyResults(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("未知意见征集命令 %q；使用 survey --help 查看用法", args[0])
	}
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func runSurveyList(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("survey list", stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy survey list [--admin-url URL]")
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	client, err := newAdminClient(*adminURL)
	if err != nil {
		return err
	}
	return client.request(http.MethodGet, "/v1/admin/surveys", nil, stdout)
}

func runSurveyCreate(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("survey create", stderr)
	file := flags.String("file", "", "意见征集 JSON 文件路径；使用 - 从标准输入读取")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy survey create --file <路径|-> [--admin-url URL]")
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
	return client.request(http.MethodPost, "/v1/admin/surveys", body, stdout)
}

func runSurveyUpdate(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("survey update", stderr)
	key := flags.String("key", "", "要更新的意见征集 key")
	file := flags.String("file", "", "意见征集 JSON 文件路径；使用 - 从标准输入读取")
	flags.Usage = func() {
		fmt.Fprintln(
			stderr,
			"用法: els-feedback-proxy survey update --key KEY --file <路径|-> [--admin-url URL]",
		)
	}
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if strings.TrimSpace(*key) == "" || strings.TrimSpace(*file) == "" {
		return errors.New("必须提供 --key 和 --file")
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
		"/v1/admin/surveys/"+url.PathEscape(*key),
		body,
		stdout,
	)
}

func runSurveyDelete(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("survey delete", stderr)
	key := flags.String("key", "", "要删除的意见征集 key")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy survey delete --key KEY [--admin-url URL]")
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
		"/v1/admin/surveys/"+url.PathEscape(*key),
		nil,
		io.Discard,
	); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{"success": true, "key": *key})
}

func runSurveyResults(args []string, stdout, stderr io.Writer) error {
	flags, adminURL := newCommandFlagSet("survey results", stderr)
	key := flags.String("key", "", "要查看结果的意见征集 key")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: els-feedback-proxy survey results --key KEY [--admin-url URL]")
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
	return client.request(
		http.MethodGet,
		"/v1/admin/surveys/"+url.PathEscape(*key)+"/results",
		nil,
		stdout,
	)
}

func writeSurveyHelp(writer io.Writer) {
	fmt.Fprintln(writer, `意见征集管理命令

用法:
  els-feedback-proxy survey list
  els-feedback-proxy survey create --file <路径|->
  els-feedback-proxy survey update --key KEY --file <路径|->
  els-feedback-proxy survey delete --key KEY
  els-feedback-proxy survey results --key KEY

环境变量与 --admin-url 用法和 announcement 命令相同。`)
}
