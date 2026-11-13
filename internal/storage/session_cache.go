package storage

import (
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/fakeyanss/jt808-server-go/internal/protocol/model"
	"github.com/fakeyanss/jt808-server-go/pkg/routines"
)

var ErrSessionClosed = errors.New("session closed")

const monitorSessionCntInterval = 10 * time.Second

type SessionCache struct {
	cacheByID    map[string]*model.Session
	cacheByPhone map[string]*model.Session
	mutex        *sync.Mutex
}

var sessionCacheSingleton *SessionCache
var sessionCacheInitOnce sync.Once

// 实例化SessionCache
func getSessionCache() *SessionCache {
	sessionCacheInitOnce.Do(func() {
		sessionCacheSingleton = &SessionCache{
			cacheByID:    make(map[string]*model.Session),
			cacheByPhone: make(map[string]*model.Session),
			mutex:        &sync.Mutex{},
		}
		monitorSessionCnt() // 监控session个数
	})
	return sessionCacheSingleton
}

func monitorSessionCnt() {
	routines.GoSafe(func() {
		for {
			log.Debug().Int("total_conn_cnt", countSession()).Msg("Monitoring total conn count")
			time.Sleep(monitorSessionCntInterval)
		}
	})
}

// 通过id获取session。如果不存在，返回ErrSessionClosed
func GetSession(id string) (*model.Session, error) {
	c := getSessionCache()
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if s, ok := c.cacheByID[id]; ok {
		return s, nil
	}
	return nil, ErrSessionClosed
}

// 通过设备手机号/ID获取活跃session
func GetSessionByPhone(phone string) (*model.Session, error) {
	c := getSessionCache()
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if s, ok := c.cacheByPhone[phone]; ok {
		return s, nil
	}
	// Fallback: 如果服务端只有1个活跃连接，直接返回该活跃会话
	if len(c.cacheByID) == 1 {
		for _, s := range c.cacheByID {
			return s, nil
		}
	}
	return nil, ErrSessionClosed
}

// 绑定session与设备phone
func BindSessionPhone(sessionID, phone string) {
	if sessionID == "" || phone == "" {
		return
	}
	c := getSessionCache()
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if s, ok := c.cacheByID[sessionID]; ok {
		c.cacheByPhone[phone] = s
	}
}

// 缓存session
func StoreSession(s *model.Session) {
	c := getSessionCache()
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.cacheByID[s.ID] = s
}

// 清理session
func ClearSession(id string) {
	c := getSessionCache()
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if s, ok := c.cacheByID[id]; ok {
		for phone, ps := range c.cacheByPhone {
			if ps == s {
				delete(c.cacheByPhone, phone)
			}
		}
	}
	delete(c.cacheByID, id)
}

// 统计session个数
func countSession() int {
	c := getSessionCache()
	return len(c.cacheByID)
}
