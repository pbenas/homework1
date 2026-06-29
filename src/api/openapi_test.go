package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type rawServer struct {
	called     string
	bucket     string
	objectID   string
	middleware bool
}

func (s *rawServer) DeleteObject(w http.ResponseWriter, _ *http.Request, bucket Bucket, objectID ObjectID) {
	s.record("delete", bucket, objectID)
	w.WriteHeader(http.StatusOK)
}

func (s *rawServer) GetObject(w http.ResponseWriter, _ *http.Request, bucket Bucket, objectID ObjectID) {
	s.record("get", bucket, objectID)
	w.WriteHeader(http.StatusOK)
}

func (s *rawServer) CreateObject(w http.ResponseWriter, _ *http.Request, bucket Bucket, objectID ObjectID) {
	s.record("create", bucket, objectID)
	w.WriteHeader(http.StatusCreated)
}

func (s *rawServer) record(called, bucket, objectID string) {
	s.called = called
	s.bucket = bucket
	s.objectID = objectID
}

func TestStandardHTTPHandlers(t *testing.T) {
	tests := []struct {
		method string
		call   string
		status int
	}{
		{method: http.MethodPut, call: "create", status: http.StatusCreated},
		{method: http.MethodGet, call: "get", status: http.StatusOK},
		{method: http.MethodDelete, call: "delete", status: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.method, func(t *testing.T) {
			implementation := &rawServer{}
			handler := Handler(implementation)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, "/objects/documents/welcome", nil)
			handler.ServeHTTP(response, request)

			if response.Code != test.status || implementation.called != test.call ||
				implementation.bucket != "documents" || implementation.objectID != "welcome" {
				t.Fatalf("status=%d, server=%+v", response.Code, implementation)
			}
		})
	}
}

func TestHandlerOptions(t *testing.T) {
	implementation := &rawServer{}
	middleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			implementation.middleware = true
			next.ServeHTTP(w, r)
		})
	}
	mux := http.NewServeMux()
	handler := HandlerWithOptions(implementation, StdHTTPServerOptions{
		BaseURL:     "/api",
		BaseRouter:  mux,
		Middlewares: []MiddlewareFunc{middleware},
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/objects/bucket/id", nil)
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !implementation.middleware {
		t.Fatalf("status=%d, middleware=%v", response.Code, implementation.middleware)
	}

	HandlerFromMux(implementation, http.NewServeMux())
	baseHandler := HandlerFromMuxWithBaseURL(implementation, http.NewServeMux(), "/v1")
	response = httptest.NewRecorder()
	baseHandler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/v1/objects/bucket/id", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("base handler status = %d", response.Code)
	}
}

type strictServer struct {
	err error
}

func (s *strictServer) DeleteObject(_ context.Context, request DeleteObjectRequestObject) (DeleteObjectResponseObject, error) {
	if s.err != nil {
		return nil, s.err
	}
	if request.ObjectID == "missing" {
		return DeleteObject404Response{}, nil
	}
	return DeleteObject200Response{}, nil
}

func (s *strictServer) GetObject(_ context.Context, request GetObjectRequestObject) (GetObjectResponseObject, error) {
	if s.err != nil {
		return nil, s.err
	}
	if request.ObjectID == "missing" {
		return GetObject404Response{}, nil
	}
	return GetObject200TextResponse("contents"), nil
}

func (s *strictServer) CreateObject(_ context.Context, request CreateObjectRequestObject) (CreateObjectResponseObject, error) {
	if s.err != nil {
		return nil, s.err
	}
	if request.ObjectID == "conflict" {
		return CreateObject409JSONResponse{
			Body:    ObjectReference{Id: "original"},
			Headers: CreateObject409ResponseHeaders{Location: "/objects/bucket/original"},
		}, nil
	}
	return CreateObject201JSONResponse{Id: request.ObjectID}, nil
}

func TestStrictHTTPHandlerResponses(t *testing.T) {
	operations := map[string]bool{}
	middleware := func(next StrictHandlerFunc, operationID string) StrictHandlerFunc {
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			operations[operationID] = true
			return next(ctx, w, r, request)
		}
	}
	handler := Handler(NewStrictHandler(&strictServer{}, []StrictMiddlewareFunc{middleware}))

	tests := []struct {
		method   string
		path     string
		body     string
		status   int
		response string
		location string
	}{
		{http.MethodPut, "/objects/bucket/new", "text", 201, `{"id":"new"}` + "\n", ""},
		{http.MethodPut, "/objects/bucket/conflict", "text", 409, `{"id":"original"}` + "\n", "/objects/bucket/original"},
		{http.MethodGet, "/objects/bucket/id", "", 200, "contents", ""},
		{http.MethodGet, "/objects/bucket/missing", "", 404, "", ""},
		{http.MethodDelete, "/objects/bucket/id", "", 200, "", ""},
		{http.MethodDelete, "/objects/bucket/missing", "", 404, "", ""},
	}

	for _, test := range tests {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		handler.ServeHTTP(response, request)
		if response.Code != test.status || response.Body.String() != test.response ||
			response.Header().Get("Location") != test.location {
			t.Fatalf("%s %s: status=%d body=%q headers=%v", test.method, test.path, response.Code, response.Body.String(), response.Header())
		}
	}
	for _, operation := range []string{"CreateObject", "GetObject", "DeleteObject"} {
		if !operations[operation] {
			t.Errorf("middleware did not observe %s", operation)
		}
	}
}

func TestStrictHandlerErrors(t *testing.T) {
	implementation := &strictServer{err: errors.New("handler failed")}
	responseErrors := 0
	requestErrors := 0
	strict := NewStrictHandlerWithOptions(implementation, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, _ error) {
			requestErrors++
			w.WriteHeader(http.StatusBadRequest)
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, _ error) {
			responseErrors++
			w.WriteHeader(http.StatusInternalServerError)
		},
	})

	for _, test := range []struct {
		method string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{http.MethodPut, func(w http.ResponseWriter, r *http.Request) { strict.CreateObject(w, r, "bucket", "id") }},
		{http.MethodGet, func(w http.ResponseWriter, r *http.Request) { strict.GetObject(w, r, "bucket", "id") }},
		{http.MethodDelete, func(w http.ResponseWriter, r *http.Request) { strict.DeleteObject(w, r, "bucket", "id") }},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, "/", strings.NewReader("body"))
		test.call(response, request)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("%s status = %d", test.method, response.Code)
		}
	}
	if responseErrors != 3 {
		t.Fatalf("response errors = %d", responseErrors)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/", nil)
	request.Body = io.NopCloser(errorReader{})
	strict.CreateObject(response, request, "bucket", "id")
	if response.Code != http.StatusBadRequest || requestErrors != 1 {
		t.Fatalf("body error status=%d, request errors=%d", response.Code, requestErrors)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestGeneratedErrors(t *testing.T) {
	cause := errors.New("cause")
	tests := []struct {
		err    error
		unwrap error
		text   string
	}{
		{&UnescapedCookieParamError{ParamName: "cookie", Err: cause}, cause, "error unescaping cookie parameter 'cookie'"},
		{&UnmarshalingParamError{ParamName: "query", Err: cause}, cause, "Error unmarshaling parameter query as JSON: cause"},
		{&RequiredParamError{ParamName: "query"}, nil, "Query argument query is required, but not found"},
		{&RequiredHeaderError{ParamName: "header", Err: cause}, cause, "Header parameter header is required, but not found"},
		{&InvalidParamFormatError{ParamName: "path", Err: cause}, cause, "Invalid format for parameter path: cause"},
		{&TooManyValuesForParamError{ParamName: "query", Count: 2}, nil, "Expected one value for query, got 2"},
	}

	for _, test := range tests {
		if test.err.Error() != test.text {
			t.Errorf("Error() = %q, want %q", test.err.Error(), test.text)
		}
		if test.unwrap != nil && !errors.Is(test.err, test.unwrap) {
			t.Errorf("%T does not unwrap cause", test.err)
		}
	}
}

func TestGeneratedErrorResponses(t *testing.T) {
	deleteResponses := []struct {
		status   int
		response DeleteObjectResponseObject
	}{
		{http.StatusBadRequest, DeleteObject400Response{}},
		{http.StatusInternalServerError, DeleteObject500Response{}},
	}
	for _, test := range deleteResponses {
		response := httptest.NewRecorder()
		if err := test.response.VisitDeleteObjectResponse(response); err != nil {
			t.Fatal(err)
		}
		if response.Code != test.status {
			t.Errorf("DELETE response status = %d, want %d", response.Code, test.status)
		}
	}

	getResponses := []struct {
		status   int
		response GetObjectResponseObject
	}{
		{http.StatusBadRequest, GetObject400Response{}},
		{http.StatusInternalServerError, GetObject500Response{}},
	}
	for _, test := range getResponses {
		response := httptest.NewRecorder()
		if err := test.response.VisitGetObjectResponse(response); err != nil {
			t.Fatal(err)
		}
		if response.Code != test.status {
			t.Errorf("GET response status = %d, want %d", response.Code, test.status)
		}
	}

	createResponses := []struct {
		status   int
		response CreateObjectResponseObject
	}{
		{http.StatusBadRequest, CreateObject400Response{}},
		{http.StatusRequestEntityTooLarge, CreateObject413Response{}},
		{http.StatusUnsupportedMediaType, CreateObject415Response{}},
		{http.StatusInternalServerError, CreateObject500Response{}},
	}
	for _, test := range createResponses {
		response := httptest.NewRecorder()
		if err := test.response.VisitCreateObjectResponse(response); err != nil {
			t.Fatal(err)
		}
		if response.Code != test.status {
			t.Errorf("PUT response status = %d, want %d", response.Code, test.status)
		}
	}
}
