package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func Serve(ctx context.Context, cfg config.Config, in io.Reader, out io.Writer, dependencies ...ServerDependencies) error {
	start := time.Now()
	logMCPServer("starting", "db_path", cfg.DBPath, "pid", fmt.Sprintf("%d", os.Getpid()))
	st, err := store.OpenReadOnly(cfg.DBPath)
	if err != nil {
		logMCPServer("store_open_failed", "duration", time.Since(start).String(), "error", err.Error())
		return err
	}
	logMCPServer("store_opened", "duration", time.Since(start).String())

	server := NewWithDependencies(cfg, st, firstServerDependencies(dependencies))
	logMCPServer("ready")
	serveErr := server.Serve(ctx, in, out)
	cleanupErr := closeServerStore(server, st, 5*time.Second)
	err = errors.Join(serveErr, cleanupErr)
	if err != nil {
		logMCPServer("exiting", "duration", time.Since(start).String(), "error", err.Error())
		return err
	}
	logMCPServer("exiting", "duration", time.Since(start).String(), "error", "")
	return nil
}

func closeServerStore(server *Server, st *store.Store, timeout time.Duration) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownCtx.Err() != nil && errors.Is(shutdownErr, shutdownCtx.Err()) {
		go func() {
			_ = server.Close()
			_ = st.Close()
		}()
		return shutdownErr
	}
	return errors.Join(shutdownErr, st.Close())
}

func logMCPServer(event string, fields ...string) {
	_, _ = fmt.Fprintf(os.Stderr, "DEBUG %s mcp server event=%s", time.Now().Format("15:04:05.000"), event)
	for i := 0; i+1 < len(fields); i += 2 {
		_, _ = fmt.Fprintf(os.Stderr, " %s=%q", fields[i], fields[i+1])
	}
	_, _ = fmt.Fprintln(os.Stderr)
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	transportServer := s.withTransportCapabilities(transportCapabilities{audit: true})
	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)

	for {
		payload, err := readFrame(reader)
		if err != nil {
			if err == io.EOF {
				logMCPServer("stdin_eof")
				return nil
			}
			logMCPServer("read_failed", "error", err.Error())
			return err
		}

		response, ok := transportServer.processPayload(ctx, payload)
		if !ok {
			continue
		}
		if err := writeFrame(writer, response); err != nil {
			return err
		}
	}
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		if err == io.EOF && line == "" {
			return nil, err
		}
		if err != io.EOF {
			return nil, err
		}
	}
	line = strings.TrimRight(line, "\r\n")
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil, io.EOF
	}

	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return []byte(trimmed), nil
	}

	contentLength := -1
	for {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &contentLength); err != nil {
				return nil, fmt.Errorf("parse content length: %w", err)
			}
		}

		line, err = reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeFrame(writer *bufio.Writer, value interface{}) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(value); err != nil {
		return err
	}
	if _, err := writer.Write(buf.Bytes()); err != nil {
		return err
	}
	return writer.Flush()
}
