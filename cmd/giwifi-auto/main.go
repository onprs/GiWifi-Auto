package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onprs/GiWifi-Auto/internal/account"
	"github.com/onprs/GiWifi-Auto/internal/config"
	"github.com/onprs/GiWifi-Auto/internal/connectivity"
	"github.com/onprs/GiWifi-Auto/internal/control"
	"github.com/onprs/GiWifi-Auto/internal/daemon"
	"github.com/onprs/GiWifi-Auto/internal/eventlog"
	"github.com/onprs/GiWifi-Auto/internal/tui"
)

var version = "dev"

type checkResult struct {
	Valid               bool   `json:"valid"`
	Version             int    `json:"version"`
	AccountCount        int    `json:"account_count"`
	EnabledAccountCount int    `json:"enabled_account_count"`
	Error               string `json:"error,omitempty"`
}

type probeResult struct {
	Status        string `json:"status"`
	HTTPStatus    int    `json:"http_status"`
	Redirected    bool   `json:"redirected"`
	ErrorCategory string `json:"error_category,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}

	switch args[0] {
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	case "version":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "version 命令不接受额外参数")
			return 2
		}
		fmt.Fprintf(stdout, "giwifi-auto %s\n", version)
		return 0
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "probe":
		return runProbe(args[1:], stdout, stderr)
	case "daemon":
		return runDaemon(args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "account":
		return runAccount(args[1:], stdout, stderr)
	case "logs":
		return runLogs(args[1:], stdout, stderr)
	case "reload":
		return runReload(args[1:], stdout, stderr)
	case "tui":
		return runTUI(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "未知命令 %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: giwifi-auto check --config <path> [--json]")
		flags.PrintDefaults()
	}

	configPath := flags.String("config", "", "JSON 配置文件路径")
	jsonOutput := flags.Bool("json", false, "输出机器可读 JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "check 命令不接受位置参数")
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "check 命令需要 --config 指定配置文件")
		return 2
	}
	cfg, err := config.Load(context.Background(), *configPath)
	if err != nil {
		if *jsonOutput {
			if writeErr := writeJSON(stdout, checkResult{Error: err.Error()}, stderr); writeErr != nil {
				return 1
			}
		} else {
			fmt.Fprintf(stderr, "配置检查失败: %v\n", err)
		}
		return 1
	}

	result := checkResult{
		Valid:        true,
		Version:      cfg.Version,
		AccountCount: len(cfg.Accounts),
	}
	for _, account := range cfg.Accounts {
		if account.Enabled {
			result.EnabledAccountCount++
		}
	}

	if *jsonOutput {
		if err := writeJSON(stdout, result, stderr); err != nil {
			return 1
		}
		return 0
	}

	fmt.Fprintf(stdout, "配置有效: 版本 %d，账号 %d 个，启用 %d 个\n", result.Version, result.AccountCount, result.EnabledAccountCount)
	return 0
}

func runProbe(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: giwifi-auto probe --config <path> [--json]")
		flags.PrintDefaults()
	}

	configPath := flags.String("config", "", "JSON 配置文件路径")
	jsonOutput := flags.Bool("json", false, "输出机器可读 JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "probe 命令不接受位置参数")
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "probe 命令需要 --config 指定配置文件")
		return 2
	}
	cfg, err := config.Load(context.Background(), *configPath)
	if err != nil {
		result := probeResult{
			Status:        "error",
			ErrorCategory: string(connectivity.CategoryConfiguration),
		}
		if *jsonOutput {
			if writeErr := writeJSON(stdout, result, stderr); writeErr != nil {
				return 1
			}
		} else {
			fmt.Fprintf(stderr, "配置检查失败: %v\n", err)
		}
		return 1
	}

	requestContext, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	probe := connectivity.New()
	probe.Timeout = time.Duration(cfg.Runtime.RequestTimeoutSeconds) * time.Second
	probe.UserAgent = connectivity.DefaultUserAgent
	result, err := probe.Check(requestContext, cfg.Runtime.ConnectivityURL)
	output := probeResult{
		Status:     string(result.Status),
		HTTPStatus: result.HTTPStatus,
		Redirected: result.RedirectURL != nil,
	}
	if err != nil {
		category := connectivity.CategoryNetwork
		var probeErr *connectivity.ProbeError
		if errors.As(err, &probeErr) {
			category = probeErr.Category
		}
		output.ErrorCategory = string(category)
		if *jsonOutput {
			if writeErr := writeJSON(stdout, output, stderr); writeErr != nil {
				return 1
			}
		} else {
			fmt.Fprintf(stderr, "连通性探测失败: %s\n", probeErrorMessage(category))
		}
		return 1
	}

	if *jsonOutput {
		if writeErr := writeJSON(stdout, output, stderr); writeErr != nil {
			return 1
		}
		return 0
	}

	fmt.Fprintf(stdout, "网络状态: %s\n", probeStatusLabel(result.Status))
	if result.RedirectURL != nil {
		fmt.Fprintln(stdout, "检测到 Portal 重定向")
	}
	return 0
}

func runDaemon(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("daemon", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: giwifi-auto daemon --config <path>")
		flags.PrintDefaults()
	}
	configPath := flags.String("config", "", "JSON 配置文件路径")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "daemon 命令不接受位置参数")
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "daemon 命令需要 --config 指定配置文件")
		return 2
	}
	cfg, err := config.Load(context.Background(), *configPath)
	if err != nil {
		fmt.Fprintf(stderr, "配置检查失败: %v\n", err)
		return 1
	}
	service, err := daemon.New(cfg, *configPath, daemon.Dependencies{})
	if err != nil {
		fmt.Fprintf(stderr, "创建守护进程失败: %v\n", err)
		return 1
	}
	listener, err := control.Listen(cfg.Runtime.ControlSocket)
	if err != nil {
		fmt.Fprintf(stderr, "创建控制服务失败: %v\n", err)
		return 1
	}
	server, err := control.NewServer(listener, service)
	if err != nil {
		_ = listener.Close()
		fmt.Fprintf(stderr, "创建控制处理器失败: %v\n", err)
		return 1
	}

	requestContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	reloadSignals := make(chan os.Signal, 1)
	signal.Notify(reloadSignals, syscall.SIGHUP)
	defer signal.Stop(reloadSignals)
	if err := service.Start(requestContext); err != nil {
		_ = server.Close()
		service.Stop()
		fmt.Fprintf(stderr, "启动守护进程失败: %v\n", err)
		return 1
	}

	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(requestContext) }()
	for {
		select {
		case err := <-serveDone:
			service.Stop()
			if err != nil {
				fmt.Fprintf(stderr, "控制服务退出: %v\n", err)
				return 1
			}
			return 0
		case <-reloadSignals:
			if err := service.Reload(requestContext); err != nil {
				fmt.Fprintf(stderr, "重载配置失败: %v\n", err)
			}
		case <-requestContext.Done():
			_ = server.Close()
			service.Stop()
			if err := <-serveDone; err != nil {
				fmt.Fprintf(stderr, "控制服务退出: %v\n", err)
				return 1
			}
			return 0
		}
	}
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: giwifi-auto status --config <path> [--json]")
		flags.PrintDefaults()
	}
	configPath := flags.String("config", "", "JSON 配置文件路径")
	jsonOutput := flags.Bool("json", false, "输出机器可读 JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "status 命令不接受位置参数")
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "status 命令需要 --config 指定配置文件")
		return 2
	}

	var result daemon.StatusResponse
	if err := callControl(*configPath, daemon.MethodStatus, struct{}{}, &result); err != nil {
		fmt.Fprintf(stderr, "读取状态失败: %v\n", err)
		return 1
	}
	if *jsonOutput {
		if err := writeJSON(stdout, result, stderr); err != nil {
			return 1
		}
		return 0
	}
	for _, snapshot := range result.Accounts {
		lastResult := "-"
		if snapshot.LastResult != "" {
			lastResult = probeStatusLabel(snapshot.LastResult)
		}
		nextAttempt := "-"
		if snapshot.NextAttemptAt != nil {
			nextAttempt = snapshot.NextAttemptAt.Local().Format("15:04:05")
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", snapshot.ID, snapshot.DisplayName, accountStatusLabel(snapshot.State), lastResult, nextAttempt)
	}
	return 0
}

func runAccount(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "用法: giwifi-auto account <enable|disable|trigger> <id> --config <path>")
		return 2
	}
	action, id := args[0], args[1]
	flags := flag.NewFlagSet("account", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "JSON 配置文件路径")
	if err := flags.Parse(args[2:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "account 命令不接受额外位置参数")
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "account 命令需要 --config 指定配置文件")
		return 2
	}

	method := ""
	switch action {
	case "enable":
		method = daemon.MethodAccountEnable
	case "disable":
		method = daemon.MethodAccountDisable
	case "trigger":
		method = daemon.MethodAccountTrigger
	default:
		fmt.Fprintf(stderr, "未知账号操作 %q\n", action)
		return 2
	}
	if err := callControl(*configPath, method, daemon.AccountRequest{ID: id}, nil); err != nil {
		fmt.Fprintf(stderr, "账号操作失败: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "账号 %s 操作已执行: %s\n", id, action)
	return 0
}

func runLogs(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("logs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: giwifi-auto logs --config <path> [--limit <n>] [--json] [--follow]")
		flags.PrintDefaults()
	}
	configPath := flags.String("config", "", "JSON 配置文件路径")
	limit := flags.Int("limit", 50, "返回事件数量，0 表示全部")
	jsonOutput := flags.Bool("json", false, "输出机器可读 JSON")
	follow := flags.Bool("follow", false, "持续跟随新事件")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "logs 命令不接受位置参数")
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "logs 命令需要 --config 指定配置文件")
		return 2
	}
	if *limit < 0 || *limit > 1000 {
		fmt.Fprintln(stderr, "日志数量必须在 0 到 1000 之间")
		return 2
	}
	if *follow {
		return followLogs(*configPath, *limit, *jsonOutput, stdout, stderr)
	}

	var result []eventlog.Event
	if err := callControl(*configPath, daemon.MethodRecentLogs, daemon.LogsRequest{Limit: *limit}, &result); err != nil {
		fmt.Fprintf(stderr, "读取日志失败: %v\n", err)
		return 1
	}
	if *jsonOutput {
		if err := writeJSON(stdout, result, stderr); err != nil {
			return 1
		}
		return 0
	}
	for _, event := range result {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", event.At.Format(time.RFC3339), event.AccountID, event.State, event.Message)
	}
	return 0
}

func runTUI(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tui", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: giwifi-auto tui --config <path>")
		flags.PrintDefaults()
	}
	configPath := flags.String("config", "", "JSON 或 UCI 配置路径")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "tui 命令不接受位置参数")
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "tui 命令需要 --config 指定配置文件")
		return 2
	}
	cfg, err := config.Load(context.Background(), *configPath)
	if err != nil {
		fmt.Fprintf(stderr, "配置检查失败: %v\n", err)
		return 1
	}
	requestContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := tui.Run(requestContext, cfg.Runtime.ControlSocket, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(stderr, "启动 TUI 失败: %v\n", err)
		return 1
	}
	return 0
}

func runReload(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("reload", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "用法: giwifi-auto reload --config <path>")
		flags.PrintDefaults()
	}
	configPath := flags.String("config", "", "JSON 或 UCI 配置路径")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "reload 命令不接受位置参数")
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "reload 命令需要 --config 指定配置文件")
		return 2
	}
	if err := callControl(*configPath, daemon.MethodReload, struct{}{}, nil); err != nil {
		fmt.Fprintf(stderr, "重载配置失败: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "配置已重载")
	return 0
}

func followLogs(configPath string, limit int, jsonOutput bool, stdout, stderr io.Writer) int {
	requestContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var lastSequence uint64
	initial := true
	for {
		var events []eventlog.Event
		method := daemon.MethodWaitLogs
		params := daemon.WaitLogsRequest{AfterSequence: lastSequence, Limit: limit}
		if initial {
			method = daemon.MethodRecentLogs
			params = daemon.WaitLogsRequest{Limit: limit}
		}
		pollContext, cancel := context.WithTimeout(requestContext, 11*time.Second)
		if initial {
			err := callControlContext(pollContext, configPath, method, daemon.LogsRequest{Limit: limit}, &events)
			cancel()
			if err != nil {
				if requestContext.Err() != nil {
					return 0
				}
				fmt.Fprintf(stderr, "跟随日志失败: %v\n", err)
				return 1
			}
			initial = false
		} else {
			err := callControlContext(pollContext, configPath, method, params, &events)
			pollError := pollContext.Err()
			cancel()
			if err != nil {
				if requestContext.Err() != nil {
					return 0
				}
				var remoteErr *control.RemoteError
				if errors.As(err, &remoteErr) && remoteErr.Code == "request_cancelled" {
					continue
				}
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(pollError, context.DeadlineExceeded) {
					continue
				}
				fmt.Fprintf(stderr, "跟随日志失败: %v\n", err)
				return 1
			}
		}
		for _, event := range events {
			if event.Sequence <= lastSequence {
				continue
			}
			lastSequence = event.Sequence
			if jsonOutput {
				if err := writeJSON(stdout, event, stderr); err != nil {
					return 1
				}
			} else {
				writeEvent(stdout, event)
			}
		}
	}
}

func writeEvent(output io.Writer, event eventlog.Event) {
	fmt.Fprintf(output, "%s\t%s\t%s\t%s\n", event.At.Format(time.RFC3339), event.AccountID, event.State, event.Message)
}

func callControl(configPath, method string, params, result interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return callControlContext(ctx, configPath, method, params, result)
}

func callControlContext(ctx context.Context, configPath, method string, params, result interface{}) error {
	cfg, err := config.Load(ctx, configPath)
	if err != nil {
		return err
	}
	return control.Call(ctx, cfg.Runtime.ControlSocket, method, params, result)
}

func accountStatusLabel(state account.State) string {
	switch state {
	case account.StateDisabled:
		return "已停用"
	case account.StateIdle:
		return "等待检查"
	case account.StateChecking:
		return "检查中"
	case account.StateOffline:
		return "网络不可达"
	case account.StatePortal:
		return "发现 Portal"
	case account.StateAuthenticating:
		return "认证中"
	case account.StateAuthenticated:
		return "已认证"
	case account.StateBackoff:
		return "等待重试"
	case account.StateError:
		return "需要处理"
	default:
		return string(state)
	}
}

func probeStatusLabel(status connectivity.Status) string {
	switch status {
	case connectivity.StatusAuthenticated:
		return "已联网"
	case connectivity.StatusPortal:
		return "检测到 Portal"
	case connectivity.StatusOffline:
		return "网络不可达"
	default:
		return "未知"
	}
}

func probeErrorMessage(category connectivity.ErrorCategory) string {
	switch category {
	case connectivity.CategoryConfiguration:
		return "配置参数无效"
	case connectivity.CategoryNetwork:
		return "网络请求失败"
	case connectivity.CategoryProtocol:
		return "响应格式不符合预期"
	default:
		return "内部错误"
	}
}

func writeJSON(stdout io.Writer, value interface{}, stderr io.Writer) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintf(stderr, "写入 JSON 结果失败: %v\n", err)
		return err
	}
	return nil
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "用法: giwifi-auto <命令> [选项]")
	fmt.Fprintln(output, "命令:")
	fmt.Fprintln(output, "  check    校验 JSON 配置")
	fmt.Fprintln(output, "  probe    探测网络连通性")
	fmt.Fprintln(output, "  daemon   启动后台守护进程")
	fmt.Fprintln(output, "  status   查询账号状态")
	fmt.Fprintln(output, "  account  执行账号操作")
	fmt.Fprintln(output, "  logs     查询近期事件")
	fmt.Fprintln(output, "  reload   请求守护进程重载配置")
	fmt.Fprintln(output, "  tui      启动终端管理界面")
	fmt.Fprintln(output, "  version  输出构建版本")
	fmt.Fprintln(output, "  help     显示帮助")
}
