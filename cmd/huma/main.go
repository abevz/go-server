package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/abevz/go-server/internal/domain"
	"github.com/abevz/go-server/internal/platform"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

type GetUserInput struct {
	ID string `path:"id" maxLength:"32" example:"u-1" doc:"Unique User ID"`
}

type UserOutput struct {
	Body domain.User
}

type ListUsersOutput struct {
	Body []domain.User
}

type CreateUserInput struct {
	Body struct {
		Name  string `json:"name" minLength:"2" maxLength:"64" example:"Bob" doc:"Full name"`
		Email string `json:"email" format:"email" example:"bob@example.com" doc:"Email address"`
		Role  string `json:"role,omitempty" enum:"platform-engineer,sre,developer" default:"developer" doc:"Team role"`
	}
}

func NewRouter(svc *domain.UserService) http.Handler {
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("User API (Huma Code-First)", "1.0.0"))

	huma.Get(api, "/users", func(ctx context.Context, _ *struct{}) (*ListUsersOutput, error) {
		users, err := svc.ListUsers(ctx)
		if err != nil {
			return nil, mapHumaError(err)
		}
		return &ListUsersOutput{Body: users}, nil
	})

	huma.Get(api, "/users/{id}", func(ctx context.Context, in *GetUserInput) (*UserOutput, error) {
		u, err := svc.GetUser(ctx, in.ID)
		if err != nil {
			return nil, mapHumaError(err)
		}
		return &UserOutput{Body: u}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "create-user",
		Method:        http.MethodPost,
		Path:          "/users",
		Summary:       "Create a new user",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *CreateUserInput) (*UserOutput, error) {
		u, err := svc.CreateUser(ctx, in.Body.Name, in.Body.Email, in.Body.Role)
		if err != nil {
			return nil, mapHumaError(err)
		}
		return &UserOutput{Body: u}, nil
	})

	return mux
}

func mapHumaError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, domain.ErrInvalidInput):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, domain.ErrConflict):
		return huma.Error409Conflict(err.Error())
	default:
		slog.Error("unhandled error in huma handler", "err", err)
		return huma.Error500InternalServerError("internal server error")
	}
}

func main() {
	addr := ":8082"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	svc := domain.NewUserService()
	if err := platform.ListenAndServeGraceful("huma", addr, NewRouter(svc)); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
