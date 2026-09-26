package database

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/chenliitaz/chatlog/internal/chatlog/conf"
	"github.com/chenliitaz/chatlog/internal/chatlog/webhook"
	"github.com/chenliitaz/chatlog/internal/errors"
	"github.com/chenliitaz/chatlog/internal/model"
	"github.com/chenliitaz/chatlog/internal/wechatdb"
)

const (
	StateInit = iota
	StateDecrypting
	StateReady
	StateError
)

type Service struct {
	mu            sync.RWMutex // 保护 state、stateMsg、db
	state         int
	stateMsg      string
	conf          Config
	db            *wechatdb.DB
	webhook       *webhook.Service
	webhookCancel context.CancelFunc
}

type Config interface {
	GetWorkDir() string
	GetPlatform() string
	GetVersion() int
	GetWebhook() *conf.Webhook
}

func NewService(conf Config) *Service {
	return &Service{
		conf:    conf,
		webhook: webhook.New(conf),
	}
}

func (s *Service) Start() error {
	db, err := wechatdb.New(s.conf.GetWorkDir(), s.conf.GetPlatform(), s.conf.GetVersion())
	if err != nil {
		return err
	}
	s.SetReady()
	s.mu.Lock()
	s.db = db
	s.mu.Unlock()
	s.initWebhook()
	return nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	db := s.db
	s.db = nil
	s.state = StateInit
	s.stateMsg = ""
	s.mu.Unlock()
	if db != nil {
		db.Close()
	}
	if s.webhookCancel != nil {
		s.webhookCancel()
		s.webhookCancel = nil
	}
	return nil
}

func (s *Service) SetInit() {
	s.mu.Lock()
	s.state = StateInit
	s.mu.Unlock()
}

func (s *Service) SetDecrypting() {
	s.mu.Lock()
	s.state = StateDecrypting
	s.mu.Unlock()
}

func (s *Service) SetReady() {
	s.mu.Lock()
	s.state = StateReady
	s.mu.Unlock()
}

func (s *Service) SetError(msg string) {
	s.mu.Lock()
	s.state = StateError
	s.stateMsg = msg
	s.mu.Unlock()
}

func (s *Service) GetState() (int, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state, s.stateMsg
}

func (s *Service) GetDB() *wechatdb.DB {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db
}

func (s *Service) GetMessages(start, end time.Time, talker string, sender string, keyword string, limit, offset int) ([]*model.Message, error) {
	db := s.GetDB()
	if db == nil {
		return nil, errors.ErrDBNotReady
	}
	return db.GetMessages(start, end, talker, sender, keyword, limit, offset)
}

func (s *Service) GetContacts(key string, limit, offset int) (*wechatdb.GetContactsResp, error) {
	db := s.GetDB()
	if db == nil {
		return nil, errors.ErrDBNotReady
	}
	return db.GetContacts(key, limit, offset)
}

func (s *Service) GetChatRooms(key string, limit, offset int) (*wechatdb.GetChatRoomsResp, error) {
	db := s.GetDB()
	if db == nil {
		return nil, errors.ErrDBNotReady
	}
	return db.GetChatRooms(key, limit, offset)
}

// GetSession retrieves session information
func (s *Service) GetSessions(key string, limit, offset int) (*wechatdb.GetSessionsResp, error) {
	db := s.GetDB()
	if db == nil {
		return nil, errors.ErrDBNotReady
	}
	return db.GetSessions(key, limit, offset)
}

func (s *Service) GetMedia(_type string, key string) (*model.Media, error) {
	db := s.GetDB()
	if db == nil {
		return nil, errors.ErrDBNotReady
	}
	return db.GetMedia(_type, key)
}

func (s *Service) initWebhook() error {
	if s.webhook == nil {
		return nil
	}
	db := s.GetDB()
	if db == nil {
		return errors.ErrDBNotReady
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.webhookCancel = cancel
	hooks := s.webhook.GetHooks(ctx, db)
	for _, hook := range hooks {
		log.Info().Msgf("set callback %#v", hook)
		if err := db.SetCallback(hook.Group(), hook.Callback); err != nil {
			log.Error().Err(err).Msgf("set callback %#v failed", hook)
			return err
		}
	}
	return nil
}

// Close closes the database connection
func (s *Service) Close() {
	// Add cleanup code if needed
	s.mu.Lock()
	db := s.db
	s.db = nil
	s.mu.Unlock()
	if db != nil {
		db.Close()
	}
	if s.webhookCancel != nil {
		s.webhookCancel()
		s.webhookCancel = nil
	}
}

func (s *Service) GetBizMessages(ctx context.Context, ghID string, start, end time.Time, limit int) ([]*model.BizMessage, error) {
	db := s.GetDB()
	if db == nil {
		return nil, errors.ErrDBNotReady
	}
	return db.GetBizMessages(ctx, ghID, start, end, limit)
}
