package backend

import (
	"reflect"
	"testing"
)

// TestSongFileAvailableTiers 锁住歌曲详情文件大小到播放器档位的映射：
// size_new 的上游下标一旦错位，菜单会把不存在的音质列出来（或漏掉高阶档）。
func TestSongFileAvailableTiers(t *testing.T) {
	tests := []struct {
		name string
		file SongFile
		want []string
	}{
		{
			name: "standard only",
			file: SongFile{Size96Ogg: 1, Size128MP3: 1, Size192Ogg: 1, Size320MP3: 1},
			want: []string{"96ogg", "128", "192ogg", "320"},
		},
		{
			name: "lossless and modern tiers",
			file: SongFile{
				Size128MP3: 1,
				SizeFlac:   1,
				SizeNew:    []int64{1, 1, 1, 1, 1, 1, 1},
			},
			want: []string{"128", "320ogg", "640ogg", "flac", "atmos2", "atmos51", "atmos71", "master"},
		},
		{
			name: "short size_new",
			file: SongFile{Size128MP3: 1, SizeNew: []int64{0, 1}},
			want: []string{"128", "atmos2"},
		},
		{
			name: "missing sizes",
			file: SongFile{},
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.file.AvailableTiers(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("AvailableTiers() = %v, want %v", got, tt.want)
			}
		})
	}
}
