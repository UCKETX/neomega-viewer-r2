// viewer 组装文件来源、统一射线核心与流式浏览页面。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"neomega-converter-core/structure"
	"neomega-r2link/blockactor"
	"neomega-r2link/effector"
	app "neomega-viewer-r2"
	"neomega-viewer-r2/internal/server"
	"neomega-viewer-r2/internal/snapshot"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"
)

// version 由发布构建通过 -ldflags "-X main.version=v…" 注入，本地构建显示 dev。
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 在资源准备完成且端口监听成功后显示地址，退出时等待正在处理的请求结束。
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) > 1 && os.Args[1] == "export" {
		if err := snapshot.Run(ctx, os.Args[2:]); err != flag.ErrHelp {
			return err
		}
		return nil
	}
	listen := flag.String("listen", "0.0.0.0:2412", "监听地址")
	world := flag.String("world", "", "启动时打开 Java、Bedrock、生存战争 .scworld、PackFS 或结构文件，留空显示 Möbius")
	scTexture := flag.String("survivalcraft-texture", "", "生存战争方块图集（PNG 或 PNG 格式的 .scbtex）")
	var scMods []string
	flag.Func("survivalcraft-mod", "生存战争 .scmod 路径（可重复，仅读取静态资源）", func(path string) error {
		scMods = append(scMods, path)
		return nil
	})
	dimension := flag.String("dimension", "", "Java 维度目录或 Bedrock 维度 0/1/2，默认主世界")
	bedrockFix := flag.Bool("bedrock-fix", true, "修复 Bedrock 转换后的邻接属性")
	structureMemory := flag.Int("structure-memory-mib", 512, "结构转换工作内存预算（MiB，至少 64）")
	structureWorkers := flag.Int("structure-workers", 0, "结构写出并发上限，0 自动选择")
	webPath := flag.String("web", "", "开发时读取应用网页目录")
	desktopHost := flag.Bool("desktop-host", false, "桌面宿主模式：stdout 上报 JSON 就绪消息，stdin 关闭时退出，不启动外部浏览器")
	showVersion := flag.Bool("version", false, "打印版本号后退出")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	colored := !*desktopHost && terminalColors()
	if !*desktopHost {
		printBanner(os.Stdout, version, colored)
	}
	if *desktopHost {
		// 宿主独占 stdin 管道；正常关闭或宿主崩溃都通过 EOF 释放服务。
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			stop()
		}()
	}
	// 新的特效和 NBT 方块处理器在这里显式组装，核心不决定应用启用哪些内容。
	service := server.New(
		effector.Registry{"minecraft:enchanting_table": effector.Kind("enchanting_book")},
		blockactor.Registry{},
	)
	service.DesktopHost = *desktopHost
	service.Options.SkipBedrockFix = !*bedrockFix
	service.Options.SurvivalcraftTexture = *scTexture
	service.Options.SurvivalcraftMods = scMods
	service.Options.Structure = structure.Options{MemoryMiB: *structureMemory, Workers: *structureWorkers}
	defer service.Close()
	if *world != "" {
		if _, err := service.Open(ctx, server.OpenOptions{Path: *world, Dimension: *dimension}); err != nil {
			return err
		}
	}
	web, err := fs.Sub(app.Web, "web")
	if err != nil {
		return err
	}
	if *webPath != "" {
		web = os.DirFS(*webPath)
	}
	network := "tcp"
	host, _, _ := net.SplitHostPort(*listen)
	if net.ParseIP(host).To4() != nil {
		network = "tcp4"
	}
	listener, err := net.Listen(network, *listen)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Handler:           service.Handler(web),
		ReadHeaderTimeout: 5 * time.Second,
	}
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	address := listener.Addr().(*net.TCPAddr)
	local := address.IP.String()
	if address.IP.IsUnspecified() {
		local = "127.0.0.1"
	}
	url := "http://" + net.JoinHostPort(local, fmt.Sprint(address.Port))
	if *desktopHost {
		if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"event": "ready", "url": url}); err != nil {
			_ = httpServer.Close()
			return err
		}
	} else {
		fmt.Println("  本地    " + terminalText(colored, "1;97", url))
		for _, ip := range lanAddresses(address.IP) {
			fmt.Println(terminalText(colored, "38;5;245", fmt.Sprintf("  局域网  http://%s:%d", ip, address.Port)))
		}
		fmt.Println("\n  WebGPU 和 WebGL 都用显卡绘制世界。WebGPU 是 Viewer 的默认后端，可用于本地地址和 HTTPS。")
		fmt.Println("  WebGL 是较早的浏览器图形接口，通过普通局域网 HTTP 地址访问时使用它。")
	}
	select {
	case err := <-done:
		if err != http.ErrServerClosed {
			return err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdown)
}

// 只公布处于启用状态且有监听的 IPv4 网卡地址。
func lanAddresses(bound net.IP) []string {
	var ips []string
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil || ip.To4() == nil || ip.IsLoopback() || (!bound.IsUnspecified() && !bound.Equal(ip)) {
				continue
			}
			ips = append(ips, ip.String())
		}
	}
	slices.Sort(ips)
	return slices.Compact(ips)
}
