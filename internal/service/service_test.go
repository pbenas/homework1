package service

import (
	"context"
	"errors"
	"testing"

	"github.com/pbenas/homework1/internal/api"
	"github.com/pbenas/homework1/internal/store"
)

type fakeStore struct {
	createData []byte
	createErr  error
	getData    []byte
	getErr     error
	deleteErr  error
}

func (s *fakeStore) Create(_ context.Context, _, _ string, data []byte) error {
	s.createData = append([]byte(nil), data...)
	return s.createErr
}

func (s *fakeStore) Get(context.Context, string, string) ([]byte, error) {
	return s.getData, s.getErr
}

func (s *fakeStore) Delete(context.Context, string, string) error {
	return s.deleteErr
}

func TestCreateObject(t *testing.T) {
	body := api.CreateObjectTextRequestBody("contents")
	storage := &fakeStore{}
	service := New(storage)

	response, err := service.CreateObject(context.Background(), api.CreateObjectRequestObject{
		Bucket: "bucket", ObjectID: "id", Body: &body,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, ok := response.(api.CreateObject201JSONResponse)
	if !ok || created.Id != "id" || string(storage.createData) != "contents" {
		t.Fatalf("response = %#v, data = %q", response, storage.createData)
	}

	storage.createErr = &store.ConflictError{ExistingID: "first id"}
	response, err = service.CreateObject(context.Background(), api.CreateObjectRequestObject{
		Bucket: "bucket name", ObjectID: "second",
	})
	if err != nil {
		t.Fatal(err)
	}
	conflict, ok := response.(api.CreateObject409JSONResponse)
	if !ok || conflict.Body.Id != "first id" ||
		conflict.Headers.Location != "/objects/bucket%20name/first%20id" {
		t.Fatalf("conflict response = %#v", response)
	}

	storage.createErr = errors.New("storage failed")
	if response, err = service.CreateObject(context.Background(), api.CreateObjectRequestObject{}); err == nil || response != nil {
		t.Fatalf("response = %#v, error = %v", response, err)
	}
}

func TestGetObject(t *testing.T) {
	storage := &fakeStore{getData: []byte("contents")}
	service := New(storage)

	response, err := service.GetObject(context.Background(), api.GetObjectRequestObject{})
	if err != nil || response.(api.GetObject200TextResponse) != "contents" {
		t.Fatalf("response = %#v, error = %v", response, err)
	}

	storage.getErr = store.ErrNotFound
	if response, err = service.GetObject(context.Background(), api.GetObjectRequestObject{}); err != nil {
		t.Fatal(err)
	} else if _, ok := response.(api.GetObject404Response); !ok {
		t.Fatalf("response = %#v", response)
	}

	storage.getErr = errors.New("storage failed")
	if response, err = service.GetObject(context.Background(), api.GetObjectRequestObject{}); err == nil || response != nil {
		t.Fatalf("response = %#v, error = %v", response, err)
	}
}

func TestDeleteObject(t *testing.T) {
	storage := &fakeStore{}
	service := New(storage)

	response, err := service.DeleteObject(context.Background(), api.DeleteObjectRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := response.(api.DeleteObject200Response); !ok {
		t.Fatalf("response = %#v", response)
	}

	storage.deleteErr = store.ErrNotFound
	if response, err = service.DeleteObject(context.Background(), api.DeleteObjectRequestObject{}); err != nil {
		t.Fatal(err)
	} else if _, ok := response.(api.DeleteObject404Response); !ok {
		t.Fatalf("response = %#v", response)
	}

	storage.deleteErr = errors.New("storage failed")
	if response, err = service.DeleteObject(context.Background(), api.DeleteObjectRequestObject{}); err == nil || response != nil {
		t.Fatalf("response = %#v, error = %v", response, err)
	}
}
