package session

import (
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"localsend-cli/internal/localsend/constants"
	"localsend-cli/internal/models"
)

// MaxConcurrentSessions is the maximum number of concurrent receive sessions
const MaxConcurrentSessions = 100

type RecvSessManager struct {
	sessions    *sync.Map
	done        chan struct{}
	stopOnce    sync.Once
	admissionMu sync.Mutex
}

func NewRecvSessManager() *RecvSessManager {
	return &RecvSessManager{
		sessions: &sync.Map{},
		done:     make(chan struct{}),
	}
}

func (rsm *RecvSessManager) Start() {
	go rsm.vacuumTask()
}

func (rsm *RecvSessManager) Stop() {
	rsm.stopOnce.Do(func() {
		close(rsm.done)
	})
}

func (rsm *RecvSessManager) vacuumTask() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-rsm.done:
			return
		case <-ticker.C:
			rsm.sessions.Range(func(key, value any) bool {
				sessionId, ok := key.(string)
				if !ok {
					return true // skip invalid entry
				}
				session, ok := value.(*RecvSession)
				if !ok {
					return true // skip invalid entry
				}

				if session.Stopped() {
					slog.Info("Cleanup stopped session", "session", sessionId)
					rsm.sessions.Delete(sessionId)
				}

				return true
			})
		}
	}
}

func (rsm *RecvSessManager) GeneratePreUploadResp(sessionId string) (models.PreUploadResp, error) {
	sess, err := rsm.GetSession(sessionId)
	if err != nil {
		return models.PreUploadResp{}, err
	}

	var resp models.PreUploadResp
	resp.Tokens = sess.FileTokens()
	resp.SessionId = sessionId

	return resp, nil
}

func (rsm *RecvSessManager) NewSession(reqFiles models.FileMetas, clientIP string) (string, error) {
	rsm.admissionMu.Lock()
	defer rsm.admissionMu.Unlock()
	return rsm.newSessionLocked(reqFiles, clientIP)
}

// CreateSessionIfAllowed atomically enforces admission rules and creates a session.
// It prevents races between "has active session?" and session creation.
func (rsm *RecvSessManager) CreateSessionIfAllowed(reqFiles models.FileMetas, clientIP string) (string, error) {
	rsm.admissionMu.Lock()
	defer rsm.admissionMu.Unlock()
	if len(reqFiles) == 0 {
		return "", constants.ErrInvalidBody
	}

	if rsm.hasActiveSessionsLocked() {
		return "", constants.ErrBlockedByOthers
	}

	return rsm.newSessionLocked(reqFiles, clientIP)
}

// KillSessionForClient cancels a session only for the client that created it.
func (rsm *RecvSessManager) KillSessionForClient(sessionID, clientIP string) error {
	sess, err := rsm.GetSession(sessionID)
	if err != nil {
		if err == constants.ErrNotFound {
			return nil
		}
		return err
	}
	if sess.clientIP != "" && sess.clientIP != clientIP {
		return constants.ErrRejected
	}
	rsm.KillSession(sessionID)
	return nil
}

func (rsm *RecvSessManager) newSessionLocked(reqFiles models.FileMetas, clientIP string) (string, error) {
	if rsm.sessionCountLocked() >= MaxConcurrentSessions {
		return "", constants.ErrTooManySessions
	}

	sessionId := uuid.NewString()
	session, err := NewRecvSession(sessionId, clientIP)
	if err != nil {
		return "", err
	}

	// accept every files the client claimed
	for fileId, fileMeta := range reqFiles {
		err = session.AcceptFile(fileId, fileMeta)
		if err != nil {
			return "", err
		}
	}

	// store and start session
	rsm.sessions.Store(sessionId, session)
	session.Start()

	return sessionId, nil
}

func (rsm *RecvSessManager) sessionCountLocked() int {
	count := 0
	rsm.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

func (rsm *RecvSessManager) hasActiveSessionsLocked() bool {
	hasActive := false
	rsm.sessions.Range(func(_, value any) bool {
		session, ok := value.(*RecvSession)
		if !ok {
			return true
		}
		if !session.Stopped() {
			hasActive = true
			return false
		}
		return true
	})
	return hasActive
}

func (rsm *RecvSessManager) KillSession(sessionId string) {
	v, exist := rsm.sessions.LoadAndDelete(sessionId)
	if !exist {
		return
	}
	sess, ok := v.(*RecvSession)
	if !ok {
		return
	}
	sess.End()
}

func (rsm *RecvSessManager) GetSession(sessionId string) (*RecvSession, error) {
	v, exist := rsm.sessions.Load(sessionId)
	if !exist {
		return nil, constants.ErrNotFound
	}
	session, ok := v.(*RecvSession)
	if !ok {
		return nil, constants.ErrNotFound
	}

	return session, nil
}

// HasActiveSessions returns true if there are any active (non-stopped) sessions
// per protocol spec Section 4.1: return 409 when "Blocked by another session"
func (rsm *RecvSessManager) HasActiveSessions() bool {
	hasActive := false
	rsm.sessions.Range(func(key, value any) bool {
		session, ok := value.(*RecvSession)
		if !ok {
			return true // skip invalid entry
		}
		if !session.Stopped() {
			hasActive = true
			return false // stop iteration
		}
		return true
	})
	return hasActive
}
