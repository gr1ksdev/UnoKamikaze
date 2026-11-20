package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mymmrac/telego"
)

const (
	avatarDownloadTimeout = 5 * time.Second
	avatarMaxBytes        = 2 << 20 // 2 MiB is plenty for a profile picture
	avatarCacheTTL        = 5 * time.Minute
	avatarCacheLimit      = 256
)

// AvatarFetcher supplies the requesting user's profile photo, if any. A nil
// result with a nil error means the user has no photo and the card should fall
// back to initials.
type AvatarFetcher interface {
	Fetch(ctx context.Context, userID int64) ([]byte, error)
}

// profilePhotosAPI is the small slice of the Telegram API used to locate a
// profile photo. *telego.Bot implements it.
type profilePhotosAPI interface {
	GetUserProfilePhotos(ctx context.Context, params *telego.GetUserProfilePhotosParams) (*telego.UserProfilePhotos, error)
	GetFile(ctx context.Context, params *telego.GetFileParams) (*telego.File, error)
	FileDownloadURL(filepath string) string
}

type avatarCacheEntry struct {
	data    []byte
	expires time.Time
}

type telegramAvatarFetcher struct {
	api    profilePhotosAPI
	client *http.Client

	mu    sync.Mutex
	cache map[int64]avatarCacheEntry
}

// NewAvatarFetcher builds the production avatar fetcher. The HTTP client is
// bounded so a slow file download cannot hold a chat worker indefinitely.
// Photos are cached briefly: a stats card miss caused only by a new score does
// not need to download the same profile picture again.
func NewAvatarFetcher(api profilePhotosAPI, client *http.Client) AvatarFetcher {
	if client == nil {
		client = &http.Client{Timeout: avatarDownloadTimeout}
	}
	return &telegramAvatarFetcher{api: api, client: client, cache: make(map[int64]avatarCacheEntry)}
}

func (f *telegramAvatarFetcher) cached(userID int64) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.cache[userID]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expires) {
		delete(f.cache, userID)
		return nil, false
	}
	return entry.data, true
}

func (f *telegramAvatarFetcher) remember(userID int64, data []byte) {
	if data == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.cache) >= avatarCacheLimit {
		var oldest int64
		var earliest time.Time
		for id, entry := range f.cache {
			if earliest.IsZero() || entry.expires.Before(earliest) {
				oldest, earliest = id, entry.expires
			}
		}
		delete(f.cache, oldest)
	}
	f.cache[userID] = avatarCacheEntry{
		data:    append([]byte(nil), data...),
		expires: time.Now().Add(avatarCacheTTL),
	}
}

func (f *telegramAvatarFetcher) Fetch(ctx context.Context, userID int64) ([]byte, error) {
	if data, ok := f.cached(userID); ok {
		return data, nil
	}
	photos, err := f.api.GetUserProfilePhotos(ctx, &telego.GetUserProfilePhotosParams{UserID: userID, Limit: 1})
	if err != nil {
		return nil, redactError(err)
	}
	if photos == nil || len(photos.Photos) == 0 || len(photos.Photos[0]) == 0 {
		// Remember the absence too: users without a photo are common.
		f.remember(userID, []byte{})
		return nil, nil
	}

	// Telegram orders sizes from smallest to largest. The largest is the best
	// fit for the card; the download size is capped below.
	sizes := photos.Photos[0]
	chosen := sizes[len(sizes)-1]
	file, err := f.api.GetFile(ctx, &telego.GetFileParams{FileID: chosen.FileID})
	if err != nil {
		return nil, redactError(err)
	}
	if file == nil || file.FilePath == "" {
		return nil, errors.New("telegram: profile photo has no file path")
	}

	// FileDownloadURL embeds the bot token. Never log the URL or the raw HTTP
	// error: redactError strips tokens from both.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.api.FileDownloadURL(file.FilePath), nil)
	if err != nil {
		return nil, redactError(err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, redactError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram: profile photo download returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, avatarMaxBytes+1))
	if err != nil {
		return nil, redactError(err)
	}
	if len(data) > avatarMaxBytes {
		return nil, errors.New("telegram: profile photo is too large")
	}
	f.remember(userID, data)
	return data, nil
}
