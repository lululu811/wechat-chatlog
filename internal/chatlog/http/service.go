package http

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"

	"github.com/lululu811/wechat-chatlog/internal/chatlog/bizhub"
	"github.com/lululu811/wechat-chatlog/internal/chatlog/database"
	"github.com/lululu811/wechat-chatlog/internal/errors"
)

type Service struct {
	conf Config
	db   *database.Service

	router *gin.Engine
	server *http.Server

	mcpServer           *server.MCPServer
	mcpSSEServer        *server.SSEServer
	mcpStreamableServer *server.StreamableHTTPServer

	bizhub atomic.Pointer[bizhub.Service]
}

type Config interface {
	GetHTTPAddr() string
	GetDataDir() string
	GetAuthToken() string
	GetLLMBaseURL() string
	GetLLMAPIKey() string
	GetLLMModel() string
}

func NewService(conf Config, db *database.Service) *Service {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()

	// Handle error from SetTrustedProxies
	if err := router.SetTrustedProxies(nil); err != nil {
		log.Err(err).Msg("Failed to set trusted proxies")
	}

	// Middleware
	router.Use(
		errors.RecoveryMiddleware(),
		errors.ErrorHandlerMiddleware(),
		gin.LoggerWithWriter(log.Logger, "/health"),
		corsMiddleware(),
	)

	s := &Service{
		conf:   conf,
		db:     db,
		router: router,
	}

	s.initMCPServer()
	s.initRouter()

	var llmClient *bizhub.LLMClient
	if s.conf.GetLLMAPIKey() != "" {
		llmClient = bizhub.NewLLMClient(s.conf.GetLLMBaseURL(), s.conf.GetLLMAPIKey(), s.conf.GetLLMModel())
	}
	bizhub.RegisterStatic(s.router, "/biz/static")
	bizhub.RegisterRoutes(s.router.Group("", s.authMiddleware()), s.bizhub.Load, llmClient)
	return s
}

func (s *Service) Start() error {

	if err := s.checkAddrAuth(); err != nil {
		return err
	}

	s.server = &http.Server{
		Addr:    s.conf.GetHTTPAddr(),
		Handler: s.router,
	}

	go func() {
		// Handle error from Run
		if err := s.server.ListenAndServe(); err != nil {
			log.Err(err).Msg("Failed to start HTTP server")
		}
	}()

	log.Info().Msg("Starting HTTP server on " + s.conf.GetHTTPAddr())

	return nil
}

func (s *Service) ListenAndServe() error {

	if err := s.checkAddrAuth(); err != nil {
		return err
	}

	s.server = &http.Server{
		Addr:    s.conf.GetHTTPAddr(),
		Handler: s.router,
	}

	log.Info().Msg("Starting HTTP server on " + s.conf.GetHTTPAddr())
	return s.server.ListenAndServe()
}

func (s *Service) Stop() error {

	if s.server == nil {
		return nil
	}

	// 使用超时上下文优雅关闭
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		log.Debug().Err(err).Msg("Failed to shutdown HTTP server")
		return nil
	}

	log.Info().Msg("HTTP server stopped")
	return nil
}

func (s *Service) GetRouter() *gin.Engine {
	return s.router
}

// SetBizHub 设置公众号汇总服务，路由已在 NewService 中注册，handler 动态获取
func (s *Service) SetBizHub(bh *bizhub.Service) {
	s.bizhub.Store(bh)
}

// checkAddrAuth 绑定非 loopback 地址且未配置 auth token 时拒绝启动
func (s *Service) checkAddrAuth() error {
	addr := s.conf.GetHTTPAddr()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if s.conf.GetAuthToken() == "" && !isLoopbackHost(host) {
		err := fmt.Errorf("refusing to start HTTP server on non-loopback address %q without auth_token; set auth_token in config or bind to a loopback address", addr)
		log.Error().Err(err).Msg("HTTP server start aborted")
		return err
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
