package appui

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Team-Quaver/quaver-astra/internal/backend"
)

func TestFetchAllSingerAlbumsLoadsEveryPage(t *testing.T) {
	pages := map[int][]backend.SingerAlbum{
		1: {{Mid: "a1", Name: "第一张"}, {Mid: "a2", Name: "第二张"}},
		2: {{Mid: "a3", Name: "太阳之子"}},
	}
	var calls []int
	got, err := fetchAllSingerAlbums(func(page, num int) (backend.SingerAlbums, error) {
		calls = append(calls, page)
		if num != 2 {
			t.Fatalf("page size = %d, want 2", num)
		}
		return backend.SingerAlbums{Total: 3, AlbumList: pages[page]}, nil
	}, 2)
	if err != nil {
		t.Fatal(err)
	}

	wantCalls := []int{1, 2}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("requested pages = %v, want %v", calls, wantCalls)
	}
	var names []string
	for _, album := range got.AlbumList {
		names = append(names, album.Name)
	}
	wantNames := []string{"第一张", "第二张", "太阳之子"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("album names = %v, want %v", names, wantNames)
	}
	if got.Total != 3 {
		t.Fatalf("total = %d, want 3", got.Total)
	}
}

func TestFetchAllSingerAlbumsStopsOnEmptyPageWithoutTotal(t *testing.T) {
	pages := map[int][]backend.SingerAlbum{
		1: {{Mid: "a1"}, {Mid: "a2"}},
		2: {{Mid: "a3"}, {Mid: "a4"}},
		3: nil,
	}
	var calls []int
	got, err := fetchAllSingerAlbums(func(page, num int) (backend.SingerAlbums, error) {
		calls = append(calls, page)
		return backend.SingerAlbums{AlbumList: pages[page]}, nil
	}, 2)
	if err != nil {
		t.Fatal(err)
	}

	wantCalls := []int{1, 2, 3}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("requested pages = %v, want %v", calls, wantCalls)
	}
	if len(got.AlbumList) != 4 {
		t.Fatalf("album count = %d, want 4", len(got.AlbumList))
	}
}

func TestFetchAllSingerAlbumsKeepsPagesLoadedBeforeError(t *testing.T) {
	boom := errors.New("page 2 failed")
	got, err := fetchAllSingerAlbums(func(page, num int) (backend.SingerAlbums, error) {
		if page == 1 {
			return backend.SingerAlbums{
				Total:     3,
				AlbumList: []backend.SingerAlbum{{Mid: "a1"}, {Mid: "a2"}},
			}, nil
		}
		return backend.SingerAlbums{}, boom
	}, 2)
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want %v", err, boom)
	}
	if len(got.AlbumList) != 2 {
		t.Fatalf("album count = %d, want 2", len(got.AlbumList))
	}
}
