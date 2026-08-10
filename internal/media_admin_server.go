package internal

import (
	"context"
	"fmt"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	mediaadminv1 "github.com/Muxcore-Media/contracts-media-admin/gen/muxcore/media/admin/v1"
	mgmntv1 "github.com/Muxcore-Media/media-movies/proto/mgmntv1"
)

// mediaAdminServer adapts Module to MediaAdminService without colliding with
// MovieManagementService method names (ListMissing, CreateTag, …).
type mediaAdminServer struct {
	mediaadminv1.UnimplementedMediaAdminServiceServer
	m *Module
}

func (s mediaAdminServer) GetMediaTypeInfo(ctx context.Context, req *mediaadminv1.GetMediaTypeInfoRequest) (*mediaadminv1.GetMediaTypeInfoResponse, error) {
	return s.m.GetMediaTypeInfo(ctx, req)
}

func (s mediaAdminServer) ListItems(ctx context.Context, req *mediaadminv1.ListItemsRequest) (*mediaadminv1.ListItemsResponse, error) {
	return s.m.ListItems(ctx, req)
}

func (s mediaAdminServer) GetItem(ctx context.Context, req *mediaadminv1.GetItemRequest) (*mediaadminv1.GetItemResponse, error) {
	return s.m.GetItem(ctx, req)
}

func (s mediaAdminServer) UpdateMetadata(ctx context.Context, req *mediaadminv1.UpdateMetadataRequest) (*mediaadminv1.UpdateMetadataResponse, error) {
	return s.m.UpdateMetadata(ctx, req)
}

func (s mediaAdminServer) ListArtwork(ctx context.Context, req *mediaadminv1.ListArtworkRequest) (*mediaadminv1.ListArtworkResponse, error) {
	return s.m.ListArtwork(ctx, req)
}

func (s mediaAdminServer) ReplaceArtwork(stream mediaadminv1.MediaAdminService_ReplaceArtworkServer) error {
	return s.m.ReplaceArtwork(stream)
}

func (s mediaAdminServer) DeleteItem(ctx context.Context, req *mediaadminv1.DeleteItemRequest) (*mediaadminv1.DeleteItemResponse, error) {
	return s.m.DeleteItem(ctx, req)
}

func (s mediaAdminServer) RefreshItem(ctx context.Context, req *mediaadminv1.RefreshItemRequest) (*mediaadminv1.RefreshItemResponse, error) {
	return s.m.RefreshItem(ctx, req)
}

func (s mediaAdminServer) SearchIndexers(ctx context.Context, req *mediaadminv1.SearchIndexersRequest) (*mediaadminv1.SearchIndexersResponse, error) {
	return s.m.SearchIndexers(ctx, req)
}

func (s mediaAdminServer) ListHistory(ctx context.Context, req *mediaadminv1.ListHistoryRequest) (*mediaadminv1.ListHistoryResponse, error) {
	return s.m.ListHistory(ctx, req)
}

func (s mediaAdminServer) ListMissing(ctx context.Context, req *mediaadminv1.ListMissingRequest) (*mediaadminv1.ListMissingResponse, error) {
	resp, err := s.m.ListMissing(ctx, &mgmntv1.ListMissingRequest{
		Page: req.GetPage(), PageSize: req.GetPageSize(),
	})
	if err != nil {
		return nil, err
	}
	items := make([]*mediaadminv1.MissingItem, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		items = append(items, &mediaadminv1.MissingItem{
			Id: it.GetMovieId(), ParentId: it.GetMovieId(),
			Title: it.GetTitle(), Year: it.GetYear(),
			Metadata: map[string]string{
				"tmdb_id":            strconv.Itoa(int(it.GetTmdbId())),
				"quality_profile_id": it.GetQualityProfileId(),
				"root_folder_path":   it.GetRootFolderPath(),
			},
		})
	}
	return &mediaadminv1.ListMissingResponse{
		Items: items, Total: resp.GetTotal(),
		Page: resp.GetPage(), PageSize: resp.GetPageSize(),
	}, nil
}

func (s mediaAdminServer) ListTags(ctx context.Context, req *mediaadminv1.ListTagsRequest) (*mediaadminv1.ListTagsResponse, error) {
	resp, err := s.m.ListTags(ctx, &mgmntv1.ListTagsRequest{})
	if err != nil {
		return nil, err
	}
	tags := make([]*mediaadminv1.Tag, 0, len(resp.GetTags()))
	for _, t := range resp.GetTags() {
		tags = append(tags, &mediaadminv1.Tag{
			Id: t.GetId(), Label: t.GetLabel(), CreatedAt: t.GetCreatedAt(),
		})
	}
	return &mediaadminv1.ListTagsResponse{Tags: tags}, nil
}

func (s mediaAdminServer) CreateTag(ctx context.Context, req *mediaadminv1.CreateTagRequest) (*mediaadminv1.CreateTagResponse, error) {
	resp, err := s.m.CreateTag(ctx, &mgmntv1.CreateTagRequest{Label: req.GetLabel()})
	if err != nil {
		return nil, err
	}
	return &mediaadminv1.CreateTagResponse{TagId: resp.GetTagId()}, nil
}

func (s mediaAdminServer) DeleteTag(ctx context.Context, req *mediaadminv1.DeleteTagRequest) (*mediaadminv1.DeleteTagResponse, error) {
	_, err := s.m.DeleteTag(ctx, &mgmntv1.DeleteTagRequest{TagId: req.GetTagId()})
	if err != nil {
		return nil, err
	}
	return &mediaadminv1.DeleteTagResponse{}, nil
}

func (s mediaAdminServer) SetItemTags(ctx context.Context, req *mediaadminv1.SetItemTagsRequest) (*mediaadminv1.SetItemTagsResponse, error) {
	_, err := s.m.SetItemTags(ctx, &mgmntv1.SetItemTagsRequest{
		ItemId: req.GetItemId(), TagIds: req.GetTagIds(),
	})
	if err != nil {
		return nil, err
	}
	return &mediaadminv1.SetItemTagsResponse{}, nil
}

func (s mediaAdminServer) ListCollections(ctx context.Context, req *mediaadminv1.ListCollectionsRequest) (*mediaadminv1.ListCollectionsResponse, error) {
	resp, err := s.m.ListCollections(ctx, &mgmntv1.ListCollectionsRequest{})
	if err != nil {
		return nil, err
	}
	cols := make([]*mediaadminv1.CollectionSummary, 0, len(resp.GetCollections()))
	for _, c := range resp.GetCollections() {
		cols = append(cols, &mediaadminv1.CollectionSummary{
			Id:   strconv.Itoa(int(c.GetCollectionId())),
			Name: c.GetName(), ItemCount: c.GetMovieCount(),
		})
	}
	return &mediaadminv1.ListCollectionsResponse{Collections: cols}, nil
}

func (s mediaAdminServer) GetCollectionItems(ctx context.Context, req *mediaadminv1.GetCollectionItemsRequest) (*mediaadminv1.GetCollectionItemsResponse, error) {
	id, err := strconv.Atoi(req.GetCollectionId())
	if err != nil || id == 0 {
		return nil, fmt.Errorf("invalid collection_id")
	}
	resp, err := s.m.GetCollectionMovies(ctx, &mgmntv1.GetCollectionMoviesRequest{CollectionId: int32(id)})
	if err != nil {
		return nil, err
	}
	items := make([]*mediaadminv1.MediaItem, 0, len(resp.GetMovies()))
	for _, movie := range resp.GetMovies() {
		items = append(items, s.m.movieToMediaItem(movie))
	}
	return &mediaadminv1.GetCollectionItemsResponse{
		CollectionId: req.GetCollectionId(),
		Name:         resp.GetName(),
		Items:        items,
	}, nil
}

func (s mediaAdminServer) GetCalendar(ctx context.Context, req *mediaadminv1.GetCalendarRequest) (*mediaadminv1.GetCalendarResponse, error) {
	return nil, status.Error(codes.Unimplemented, "calendar is not supported for movies")
}
