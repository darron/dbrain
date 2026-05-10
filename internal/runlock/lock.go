package runlock

import "errors"

var ErrAlreadyLocked = errors.New("run lock already held")

type Lock struct {
	path string
	file fileLock
}

func Acquire(path string, metadata string) (*Lock, error) {
	file, err := acquireFileLock(path, metadata)
	if err != nil {
		return nil, err
	}
	return &Lock{path: path, file: file}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.close()
	l.file = nil
	return err
}

func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

type fileLock interface {
	close() error
}
