package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
)

type fakeProfilePhotosAPI struct {
	photos   *telego.UserProfilePhotos
	file     *telego.File
	err      error
	baseURL  string
	userID   int64
	getCalls int
}

func (f *fakeProfilePhotosAPI) GetUserProfilePhotos(_ context.Context, params *telego.GetUserProfilePhotosParams) (*telego.UserProfilePhotos, error) {
	f.userID = params.UserID
	f.getCalls++
	return f.photos, f.err
}

func (f *fakeProfilePhotosAPI) GetFile(_ context.Context, _ *telego.GetFileParams) (*telego.File, error) {
	return f.file, f.err
}

func (f *fakeProfilePhotosAPI) FileDownloadURL(filepath string) string {
	return f.baseURL + "/" + filepath
}

func TestAvatarFetcherDownloadsLargestProfilePhoto(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("photo-bytes"))
	}))
	defer server.Close()

	api := &fakeProfilePhotosAPI{
		photos: &telego.UserProfilePhotos{Photos: [][]telego.PhotoSize{{
			{FileID: "small", Width: 64},
			{FileID: "large", Width: 640},
		}}},
		file:    &telego.File{FilePath: "photos/file_42.jpg"},
		baseURL: server.URL,
	}
	got, err := NewAvatarFetcher(api, server.Client()).Fetch(context.Background(), 42)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(got) != "photo-bytes" {
		t.Fatalf("got %q", got)
	}
	if api.userID != 42 {
		t.Fatalf("user = %d", api.userID)
	}
}

func TestAvatarFetcherWithoutPhoto(t *testing.T) {
	api := &fakeProfilePhotosAPI{photos: &telego.UserProfilePhotos{}}
	got, err := NewAvatarFetcher(api, nil).Fetch(context.Background(), 1)
	if err != nil || got != nil {
		t.Fatalf("got %q, %v; want nil, nil", got, err)
	}
}

func TestAvatarFetcherCachesPhotoAndAbsence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("photo-bytes"))
	}))
	defer server.Close()

	api := &fakeProfilePhotosAPI{
		photos:  &telego.UserProfilePhotos{Photos: [][]telego.PhotoSize{{{FileID: "x"}}}},
		file:    &telego.File{FilePath: "photos/x.jpg"},
		baseURL: server.URL,
	}
	fetcher := NewAvatarFetcher(api, server.Client())
	for i := 0; i < 2; i++ {
		got, err := fetcher.Fetch(context.Background(), 77)
		if err != nil || string(got) != "photo-bytes" {
			t.Fatalf("call %d: got %q, %v", i, got, err)
		}
	}
	if api.getCalls != 1 {
		t.Fatalf("profile photos fetched %d times, want 1", api.getCalls)
	}

	empty := &fakeProfilePhotosAPI{photos: &telego.UserProfilePhotos{}}
	fetcher = NewAvatarFetcher(empty, nil)
	for i := 0; i < 2; i++ {
		got, err := fetcher.Fetch(context.Background(), 88)
		if err != nil || got != nil {
			t.Fatalf("absence call %d: got %q, %v", i, got, err)
		}
	}
	if empty.getCalls != 1 {
		t.Fatalf("absence fetched %d times, want 1", empty.getCalls)
	}
}

func TestAvatarFetcherRedactsTokenFromDownloadErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer server.Close()

	api := &fakeProfilePhotosAPI{
		photos:  &telego.UserProfilePhotos{Photos: [][]telego.PhotoSize{{{FileID: "x"}}}},
		file:    &telego.File{FilePath: "photos/x.jpg"},
		baseURL: server.URL,
	}
	_, err := NewAvatarFetcher(api, server.Client()).Fetch(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "bot") || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error leaks token or URL: %v", err)
	}
}
