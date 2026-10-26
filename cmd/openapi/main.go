package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/abevz/go-server/internal/domain"
	"github.com/abevz/go-server/internal/platform"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// openapiServer implements the generated StrictServerInterface.
// Notice there is zero http.ResponseWriter or json.NewEncoder here!
type openapiServer struct {
	svc *domain.UserService
}

func (s *openapiServer) ListUsers(ctx context.Context, _ ListUsersRequestObject) (ListUsersResponseObject, error) {
	users, err := s.svc.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := make(ListUsers200JSONResponse, 0, len(users))
	for _, u := range users {
		out = append(out, userSchema(u))
	}
	return out, nil
}

func (s *openapiServer) GetUser(ctx context.Context, req GetUserRequestObject) (GetUserResponseObject, error) {
	u, err := s.svc.GetUser(ctx, req.Id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return GetUser404JSONResponse{Error: err.Error()}, nil
		}
		return nil, err
	}
	return GetUser200JSONResponse(userSchema(u)), nil
}

func (s *openapiServer) CreateUser(ctx context.Context, req CreateUserRequestObject) (CreateUserResponseObject, error) {
	role := ""
	if req.Body.Role != nil {
		role = *req.Body.Role
	}
	u, err := s.svc.CreateUser(ctx, req.Body.Name, string(req.Body.Email), role)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidInput):
			return CreateUser400JSONResponse{Error: err.Error()}, nil
		case errors.Is(err, domain.ErrConflict):
			return CreateUser409JSONResponse{Error: err.Error()}, nil
		default:
			return nil, err
		}
	}
	return CreateUser201JSONResponse(userSchema(u)), nil
}

func userSchema(u domain.User) User {
	return User{Id: u.ID, Name: u.Name, Email: openapi_types.Email(u.Email), Role: u.Role, CreatedAt: u.CreatedAt}
}

func NewRouter(svc *domain.UserService) http.Handler {
	spec, err := GetSwagger()
	if err != nil {
		panic(err) // The embedded, generated contract must be valid at startup.
	}
	strict := NewStrictHandlerWithOptions(&openapiServer{svc: svc}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeAPIError(w, http.StatusBadRequest, "invalid request body")
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			slog.Error("openapi handler failed", "err", err)
			writeAPIError(w, http.StatusInternalServerError, "internal server error")
		},
	})
	mux := http.NewServeMux()
	handler := HandlerFromMux(strict, mux)
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		Options: openapi3filter.Options{
			SchemaValidationOptions: []openapi3.SchemaValidationOption{
				openapi3.EnableFormatValidation(),
				openapi3.WithStringFormatValidator("email", openapi3.NewRegexpFormatValidator(openapi3.FormatOfStringForEmail)),
			},
		},
		ErrorHandler: func(w http.ResponseWriter, message string, status int) {
			writeAPIError(w, status, message)
		},
	})
	return boundedJSONBody(validator(handler))
}

// Bound the body before schema validation, which buffers it. json.Valid also
// rejects trailing JSON values that a single Decoder.Decode would ignore.
func boundedJSONBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/users" {
			limited := http.MaxBytesReader(w, r.Body, 1<<20)
			defer limited.Close()
			body, err := io.ReadAll(limited)
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					writeAPIError(w, http.StatusRequestEntityTooLarge, "request body too large")
				} else {
					writeAPIError(w, http.StatusBadRequest, "cannot read request body")
				}
				return
			}
			if !json.Valid(body) {
				writeAPIError(w, http.StatusBadRequest, "expected a single valid JSON value")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		next.ServeHTTP(w, r)
	})
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Error: message})
}

func main() {
	addr := ":8083"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	svc := domain.NewUserService()
	if err := platform.ListenAndServeGraceful("openapi", addr, NewRouter(svc)); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}
