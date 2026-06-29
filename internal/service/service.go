// Package service implements the generated object storage API.
package service

import (
	"context"
	"errors"
	"net/url"

	"github.com/pbenas/homework1/internal/api"
	"github.com/pbenas/homework1/internal/store"
)

// ObjectStore is the storage behavior consumed by Service.
type ObjectStore interface {
	Create(context.Context, string, string, []byte) error
	Get(context.Context, string, string) ([]byte, error)
	Delete(context.Context, string, string) error
}

// Service implements the generated object-storage HTTP contract.
type Service struct {
	storage ObjectStore
}

// New creates an object-storage service backed by storage.
func New(storage ObjectStore) *Service {
	return &Service{storage: storage}
}

// CreateObject implements PUT /objects/{bucket}/{objectID}.
func (s *Service) CreateObject(ctx context.Context, request api.CreateObjectRequestObject) (api.CreateObjectResponseObject, error) {
	var data []byte
	if request.Body != nil {
		data = []byte(*request.Body)
	}
	if err := s.storage.Create(ctx, request.Bucket, request.ObjectID, data); err != nil {
		var conflict *store.ConflictError
		if errors.As(err, &conflict) {
			return api.CreateObject409JSONResponse{
				Body: api.ObjectReference{Id: conflict.ExistingID},
				Headers: api.CreateObject409ResponseHeaders{
					Location: objectLocation(request.Bucket, conflict.ExistingID),
				},
			}, nil
		}
		return nil, err
	}
	return api.CreateObject201JSONResponse{Id: request.ObjectID}, nil
}

// GetObject implements GET /objects/{bucket}/{objectID}.
func (s *Service) GetObject(ctx context.Context, request api.GetObjectRequestObject) (api.GetObjectResponseObject, error) {
	data, err := s.storage.Get(ctx, request.Bucket, request.ObjectID)
	if errors.Is(err, store.ErrNotFound) {
		return api.GetObject404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.GetObject200TextResponse(data), nil
}

// DeleteObject implements DELETE /objects/{bucket}/{objectID}.
func (s *Service) DeleteObject(ctx context.Context, request api.DeleteObjectRequestObject) (api.DeleteObjectResponseObject, error) {
	err := s.storage.Delete(ctx, request.Bucket, request.ObjectID)
	if errors.Is(err, store.ErrNotFound) {
		return api.DeleteObject404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.DeleteObject200Response{}, nil
}

func objectLocation(bucket, objectID string) string {
	return "/objects/" + url.PathEscape(bucket) + "/" + url.PathEscape(objectID)
}
