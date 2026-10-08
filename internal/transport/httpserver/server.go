package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

type Settings struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

type Server struct {
	settings Settings
	server   *http.Server
	logger   *slog.Logger
	onError  func(error)

	mu       sync.Mutex
	running  bool
	listener net.Listener
	done     chan struct{}
}

func New(handler http.Handler, settings Settings, logger *slog.Logger, onError func(error)) (*Server, error) {
	if handler == nil || settings.Address == "" || settings.ReadHeaderTimeout <= 0 || settings.ReadTimeout <= 0 ||
		settings.WriteTimeout <= 0 || settings.IdleTimeout <= 0 {
		return nil, errors.New("HTTP server requires handler, address, and positive timeouts")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{settings: settings, logger: logger, onError: onError, server: &http.Server{
		Addr: settings.Address, Handler: handler, ReadHeaderTimeout: settings.ReadHeaderTimeout,
		ReadTimeout: settings.ReadTimeout, WriteTimeout: settings.WriteTimeout, IdleTimeout: settings.IdleTimeout,
	}}, nil
}

func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return errors.New("HTTP server already started")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", s.settings.Address)
	if err != nil {
		return err
	}
	s.listener = listener
	s.done = make(chan struct{})
	s.running = true
	s.logger.Info("HTTP server listening", "address", listener.Addr().String())
	go func(done chan struct{}) {
		err := s.server.Serve(listener)
		s.mu.Lock()
		s.running = false
		close(done)
		s.mu.Unlock()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("HTTP server stopped unexpectedly", "error", err)
			if s.onError != nil {
				s.onError(err)
			}
		}
	}(s.done)
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		done := s.done
		s.mu.Unlock()
		if done != nil {
			select {
			case <-done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	done := s.done
	s.mu.Unlock()
	if err := s.server.Shutdown(ctx); err != nil {
		_ = s.server.Close()
		return err
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		_ = s.server.Close()
		return ctx.Err()
	}
}

func (s *Server) Address() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return s.settings.Address
	}
	return s.listener.Addr().String()
}

func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}
