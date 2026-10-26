package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	userv1 "github.com/abevz/go-server/gen/user/v1"
	"github.com/abevz/go-server/gen/user/v1/userv1connect"
	"github.com/abevz/go-server/internal/domain"
	"github.com/abevz/go-server/internal/platform"
)

type userServer struct {
	svc *domain.UserService
}

var _ userv1connect.UserServiceHandler = (*userServer)(nil)

func (s *userServer) GetUser(ctx context.Context, req *connect.Request[userv1.GetUserRequest]) (*connect.Response[userv1.GetUserResponse], error) {
	u, err := s.svc.GetUser(ctx, req.Msg.GetId())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&userv1.GetUserResponse{User: userProto(u)}), nil
}

func (s *userServer) CreateUser(ctx context.Context, req *connect.Request[userv1.CreateUserRequest]) (*connect.Response[userv1.CreateUserResponse], error) {
	u, err := s.svc.CreateUser(ctx, req.Msg.GetName(), req.Msg.GetEmail(), req.Msg.GetRole())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&userv1.CreateUserResponse{User: userProto(u)}), nil
}

func (s *userServer) ListUsers(ctx context.Context, _ *connect.Request[userv1.ListUsersRequest]) (*connect.Response[userv1.ListUsersResponse], error) {
	users, err := s.svc.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*userv1.User, 0, len(users))
	for _, u := range users {
		out = append(out, userProto(u))
	}
	return connect.NewResponse(&userv1.ListUsersResponse{Users: out}), nil
}

func userProto(u domain.User) *userv1.User {
	return &userv1.User{
		Id: u.ID, Name: u.Name, Email: u.Email, Role: u.Role,
		CreatedAt: u.CreatedAt.Format(time.RFC3339),
	}
}

func errorInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			resp, err := next(ctx, req)
			if err != nil {
				return nil, toConnectError(err)
			}
			return resp, nil
		}
	}
}

func toConnectError(err error) error {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, domain.ErrInvalidInput):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, domain.ErrConflict):
		return connect.NewError(connect.CodeAlreadyExists, err)
	default:
		slog.Error("unhandled rpc error", "err", err)
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
}

func NewRouter(svc *domain.UserService) http.Handler {
	mux := http.NewServeMux()
	path, handler := userv1connect.NewUserServiceHandler(&userServer{svc: svc},
		connect.WithReadMaxBytes(1<<20),
		connect.WithInterceptors(errorInterceptor()),
	)
	mux.Handle(path, handler)
	return mux
}

func main() {
	addr := ":8084"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	svc := domain.NewUserService()
	if err := platform.ListenAndServeGraceful("connect", addr, NewRouter(svc)); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
