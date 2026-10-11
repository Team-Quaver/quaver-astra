package smedia

import "testing"

func TestStateSignatureDedup(t *testing.T) {
	base := Snapshot{
		HasTrack: true,
		TrackID:  "0039MnYb0qxYhV",
		Title:    "晴天",
		Artist:   "周杰伦",
		Album:    "叶惠美",
		ArtURL:   "https://example/cover.jpg",
		Playing:  true,
		Position: 12.1,
		Duration: 269.3,
		Loop:     "all",
	}
	sig := base.stateSignature()

	// 同一整秒内的位置推进不应触发重投影。
	sameSecond := base
	sameSecond.Position = 12.9
	if got := sameSecond.stateSignature(); got != sig {
		t.Fatalf("同一秒内位置变化不应改变签名: %q != %q", got, sig)
	}

	cases := map[string]func(*Snapshot){
		"跨秒位置": func(s *Snapshot) { s.Position = 13.0 },
		"播放态":  func(s *Snapshot) { s.Playing = false },
		"曲目":   func(s *Snapshot) { s.TrackID = "other" },
		"标题":   func(s *Snapshot) { s.Title = "七里香" },
		"歌手":   func(s *Snapshot) { s.Artist = "other" },
		"专辑":   func(s *Snapshot) { s.Album = "other" },
		"封面":   func(s *Snapshot) { s.ArtURL = "https://example/other.jpg" },
		"时长":   func(s *Snapshot) { s.Duration = 269.4 },
		"循环":   func(s *Snapshot) { s.Loop = "one" },
		"无曲目":  func(s *Snapshot) { s.HasTrack = false },
	}
	for name, mutate := range cases {
		s := base
		mutate(&s)
		if got := s.stateSignature(); got == sig {
			t.Fatalf("%s 变化后签名未变", name)
		}
	}
}
