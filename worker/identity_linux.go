package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// WorkerIdentity keeps the lock open for the daemon's entire lifetime.
type WorkerIdentity struct {
	ID   string
	lock *os.File
}

func OpenWorkerIdentity(stateDir string) (*WorkerIdentity, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(stateDir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("lock worker state (another daemon may be using it): %w", err)
	}
	identity := &WorkerIdentity{lock: lock}
	if err := identity.loadOrCreate(stateDir); err != nil {
		identity.Close()
		return nil, err
	}
	return identity, nil
}

func (identity *WorkerIdentity) loadOrCreate(stateDir string) error {
	path := filepath.Join(stateDir, "worker-id")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		var uuid [16]byte
		if _, err := rand.Read(uuid[:]); err != nil {
			return err
		}
		uuid[6] = (uuid[6] & 0x0f) | 0x40
		uuid[8] = (uuid[8] & 0x3f) | 0x80
		data = fmt.Appendf(nil, "%x-%x-%x-%x-%x\n", uuid[:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:])
		temporary := path + ".tmp"
		if err := os.WriteFile(temporary, data, 0600); err != nil {
			return err
		}
		if err := os.Rename(temporary, path); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	id := strings.TrimSpace(string(data))
	if !regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`).MatchString(id) {
		return fmt.Errorf("invalid worker identity in %s; refusing to replace it", path)
	}
	identity.ID = strings.ToLower(id)
	return nil
}

func (identity *WorkerIdentity) Close() error {
	return identity.lock.Close()
}
