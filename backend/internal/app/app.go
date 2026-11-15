package app

import (
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/simajiong/jaysql/api"
	"github.com/simajiong/jaysql/internal/bootstrap"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/middleware"
	"github.com/simajiong/jaysql/pkg/secret"
	"github.com/simajiong/jaysql/web"

	aiRouter "github.com/simajiong/jaysql/internal/ai/routers"
	announcementRouter "github.com/simajiong/jaysql/internal/announcement/routers"
	backupRouter "github.com/simajiong/jaysql/internal/backup/routers"
	brandRouter "github.com/simajiong/jaysql/internal/brand/routers"
	commonRouter "github.com/simajiong/jaysql/internal/common/routers"
	dasRouter "github.com/simajiong/jaysql/internal/das/routers"
	datasecRouter "github.com/simajiong/jaysql/internal/datasec/routers"
	feedbackRouter "github.com/simajiong/jaysql/internal/feedback/routers"
	hotArticleRouter "github.com/simajiong/jaysql/internal/hotarticle/routers"
	inspectRouter "github.com/simajiong/jaysql/internal/inspect/routers"
	monitorRouter "github.com/simajiong/jaysql/internal/monitor/routers"
	ordersRouter "github.com/simajiong/jaysql/internal/orders/routers"
	ordersServices "github.com/simajiong/jaysql/internal/orders/services"
	performanceRouter "github.com/simajiong/jaysql/internal/performance/routers"
	userRouter "github.com/simajiong/jaysql/internal/users/routers"

	"github.com/gin-gonic/gin"
)

const mediaDir = "./media"

func setupStaticFiles(r *gin.Engine) error {
	// Embedded file system - 映射整个 dist 目录
	distFS, err := fs.Sub(web.StaticFS, "dist")
	if err != nil {
		return fmt.Errorf("error accessing embedded filesystem: %w", err)
	}

	// 映射 assets 目录
	assetsFS, err := fs.Sub(distFS, "assets")
	if err != nil {
		return fmt.Errorf("error accessing assets directory: %w", err)
	}
	// 资源文件名带内容 hash（如 ConfigView-C15TbKPK.js），内容变则名字变，
	// 可以长缓存；但 Vite 7 的动态 chunk hash 在模板/样式小改时可能不会变化，
	// 使用 immutable 会导致「文件名没变但内容已变」的旧资源被浏览器永久缓存。
	// 因此改为 must-revalidate：缓存期内用本地副本，过期后必须回源重新验证，
	// 既保留长缓存收益，又避免内容更新后浏览器拒不刷新。
	fileServer := http.StripPrefix("/assets", http.FileServer(http.FS(assetsFS)))
	assetsHandler := func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=31536000, must-revalidate")
		fileServer.ServeHTTP(c.Writer, c.Request)
	}
	r.GET("/assets/*filepath", assetsHandler)
	r.HEAD("/assets/*filepath", assetsHandler)

	// public 目录下的文件（avatar.png / favicon.ico / favicon.svg）
	r.StaticFileFS("/avatar.png", "avatar.png", http.FS(distFS))
	r.StaticFileFS("/favicon.ico", "favicon.ico", http.FS(distFS))
	r.StaticFileFS("/favicon.svg", "favicon.svg", http.FS(distFS))

	// 业务上传文件目录
	if _, err := os.Stat(mediaDir); os.IsNotExist(err) {
		if err := os.MkdirAll(mediaDir, os.ModePerm); err != nil {
			return fmt.Errorf("failed to create media directory: %w", err)
		}
	}
	r.Static("/media", mediaDir)

	return nil
}

func setupNoRoute(r *gin.Engine) {
	// Fix 404 issue on page refresh
	r.NoRoute(func(c *gin.Context) {
		if strings.Contains(c.Request.Header.Get("Accept"), "text/html") {
			if content, err := web.StaticFS.ReadFile("dist/index.html"); err == nil {
				// index.html 是资源引用的入口，必须禁止缓存：
				// 新版本发布后 hash 文件名全变，缓存旧 HTML 会引用到不存在的资源
				c.Header("Cache-Control", "no-cache")
				c.Header("Content-Type", "text/html; charset=utf-8")
				c.Data(http.StatusOK, "text/html; charset=utf-8", content)
				return
			}
		}
		c.String(http.StatusNotFound, "Not Found")
	})
}

func setupRootRoute(r *gin.Engine) {
	// Root route
	r.GET("/", func(c *gin.Context) {
		if data, err := web.StaticFS.ReadFile("dist/index.html"); err == nil {
			// 同 NoRoute：index.html 禁止缓存，保证发布后浏览器立即拿到新入口
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8", data)
		} else {
			_ = c.AbortWithError(http.StatusInternalServerError, err)
		}
	})
}

func RunServer() {
	// Production mode
	if global.App.Config.App.Environment == "prod" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Initialize authentication middleware
	var err error
	if global.App.JWT, err = middleware.InitAuthMiddleware(); err != nil {
		fmt.Println("Failed to initialize authentication middleware:", err)
		return
	}

	// Load route configs for multiple APPs
	api.Include(
		userRouter.Routers,
		commonRouter.Routers,
		monitorRouter.Routers,
		performanceRouter.Routers,
		inspectRouter.Routers,
		dasRouter.Routers,
		datasecRouter.Routers,
		ordersRouter.Routers,
		backupRouter.Routers,
		aiRouter.Routers,
		feedbackRouter.Routers,
		announcementRouter.Routers,
		hotArticleRouter.Routers,
		brandRouter.Routers,
	)

	// Initialize router
	r := api.Init()

	// Static files and routes
	if setupErr := setupStaticFiles(r); setupErr != nil {
		fmt.Println(setupErr)
		return
	}
	setupNoRoute(r)
	setupRootRoute(r)

	// panic 防护已挪到 api.Init()（gin 的 Use 只对之后注册的路由生效，
	// 放在全部路由注册之后的 Recovery 对现有路由不起任何作用）。

	// Start server
	addr := strings.TrimSpace(global.App.Config.App.ListenAddress)
	if addr == "" {
		addr = ":8083"
	}
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		ln, err = net.Listen("tcp", addr)
		if err != nil {
			fmt.Println("Failed to start server: ", err.Error())
			return
		}
	}
	fmt.Println("Listening and serving HTTP on", ln.Addr().String())
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}
	if serveErr := srv.Serve(ln); serveErr != nil && !strings.Contains(serveErr.Error(), "Server closed") {
		fmt.Println("Failed to start server: ", serveErr.Error())
	}
}

func Run(configFile string) {
	bootstrap.InitializeConfig(configFile)
	bootstrap.InitializeLog()
	// 凭据加解密自检必须排在任何数据库读写之前：
	// 密钥不可用时若继续启动，播种与业务写入会产出解密不了的坏数据。
	// 自检项：密钥长度 32 字节、默认密钥告警、加密/解密往返可用。
	if err := secret.SelfCheck(); err != nil {
		global.App.Log.Fatalf("secret: 凭据加解密自检失败，拒绝启动: %v", err)
	}
	// 日志脱敏：结构化字段里命中凭据类 key 的值自动打码，作为最后一道兜底
	global.App.Log.AddHook(secret.LogHook())
	global.App.DB = bootstrap.InitializeDB()
	// Redis 已从依赖中移除：工单日志推送改走进程内总线、执行锁改走进程内带租约锁。
	// 详见 pkg/eventbus 与 pkg/lock 的包注释。
	global.App.Bus = bootstrap.InitializeEventBus()
	global.App.Locks = bootstrap.InitializeLocks()
	defer func() {
		// 先停钉钉长连接再关库：长连接回调会读工单/令牌表，库先关会导致回调报错
		ordersServices.StopDingTalkApprovalStream()
		if global.App.DB != nil {
			db, _ := global.App.DB.DB()
			db.Close()
		}
	}()
	bootstrap.InitializeCron()
	// 启动钉钉审批 Stream 长连接：由我们主动出站建连，钉钉把审批卡片的
	// 「通过/转交」点击顺着这条隧道推下来，因此不需要公网 IP / 入站端口。
	// 未配置「钉钉企业应用」渠道时内部静默跳过，不影响其它功能。
	ordersServices.StartDingTalkApprovalStream()
	RunServer()
}
