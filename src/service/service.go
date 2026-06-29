// Package service implements the generated object storage API.
package service

import (
	"context"
	"errors"
	"net/url"

	"github.com/pbenas/homework1/src/api"
	"github.com/pbenas/homework1/src/store"
)

type Service struct {
	store store.Store
}

func New(storage store.Store) *Service {
	return &Service{store: storage}
}

func (s *Service) CreateObject(_ context.Context, request api.CreateObjectRequestObject) (api.CreateObjectResponseObject, error) {
	data := []byte{}
	if request.Body != nil {
		data = []byte(*request.Body)
	}
	if err := s.store.Create(request.Bucket, request.ObjectID, data); err != nil {
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

func (s *Service) GetObject(_ context.Context, request api.GetObjectRequestObject) (api.GetObjectResponseObject, error) {
	data, err := s.store.Get(request.Bucket, request.ObjectID)
	if errors.Is(err, store.ErrNotFound) {
		return api.GetObject404Response{}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.GetObject200TextResponse(data), nil
}

func (s *Service) DeleteObject(_ context.Context, request api.DeleteObjectRequestObject) (api.DeleteObjectResponseObject, error) {
	if err := s.store.Delete(request.Bucket, request.ObjectID); errors.Is(err, store.ErrNotFound) {
		return api.DeleteObject404Response{}, nil
	} else if err != nil {
		return nil, err
	}
	return api.DeleteObject200Response{}, nil
}

func objectLocation(bucket, objectID string) string {
	return "/objects/" + url.PathEscape(bucket) + "/" + url.PathEscape(objectID)
}
