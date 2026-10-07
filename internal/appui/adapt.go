package appui

import (
	"github.com/Team-Quaver/quaver-astra/internal/backend"
	"github.com/Team-Quaver/quaver-astra/internal/player"
)

// apiAdapter 把 *backend.Client 适配成 player.Backend。
type apiAdapter struct {
	c *backend.Client
}

func newAPIAdapter(c *backend.Client) apiAdapter {
	return apiAdapter{c: c}
}

func (a apiAdapter) LoginStatus() (bool, error) {
	st, err := a.c.LoginStatus()
	return st.LoggedIn, err
}

func (a apiAdapter) UserInfo() (player.UserInfo, error) {
	me, err := a.c.UserMe()
	if err != nil {
		return player.UserInfo{}, err
	}
	out := player.UserInfo{Name: me.BaseInfo.Name, Avatar: me.BaseInfo.Avatar}
	if vip, err := a.c.UserVIP(); err == nil {
		switch {
		case vip.Identity.HugeVip != 0:
			out.VipLabel = "超级会员"
		case vip.Identity.Vip >= 9:
			out.VipLabel = "豪华绿钻"
		case vip.Identity.Vip > 0:
			out.VipLabel = "绿钻"
		}
	}
	return out, nil
}

func toPlayerSong(s backend.Song) player.Song {
	out := player.Song{
		Mid:      s.Mid,
		Name:     s.Name,
		Title:    s.Title,
		Subtitle: s.Subtitle,
		Artists:  s.Artists(),
		Interval: s.Duration(),
		SongType: int64(s.Type),
	}
	if s.ID != "" {
		if v, err := s.ID.Int64(); err == nil {
			out.SongID = v
		}
	}
	if s.Album != nil {
		out.Album = s.Album.Name
		out.AlbumPmid = s.Album.Pmid
		out.AlbumMid = s.Album.Mid
	}
	return out
}

func toPlayerSongs(songs []backend.Song) []player.Song {
	out := make([]player.Song, 0, len(songs))
	for _, s := range songs {
		out = append(out, toPlayerSong(s))
	}
	return out
}

func (a apiAdapter) LikedPage(page, num int) ([]player.Song, int64, bool, error) {
	d, err := a.c.Liked(page, num)
	if err != nil {
		return nil, 0, false, err
	}
	return toPlayerSongs(d.Songs), d.Total, d.Hasmore != 0, nil
}

func (a apiAdapter) Playlists() ([]player.PlaylistRef, error) {
	res, err := a.c.CreatedSonglists()
	if err != nil {
		return nil, err
	}
	out := make([]player.PlaylistRef, 0, len(res.Playlists))
	for _, p := range res.Playlists {
		if p.DirID == 201 { // “我喜欢”虚拟歌单
			continue
		}
		out = append(out, player.PlaylistRef{ID: p.ID, Title: p.Title})
	}
	return out, nil
}

func (a apiAdapter) Tiers() (*player.TierTable, error) {
	t, err := a.c.StreamTiers()
	if err != nil {
		return nil, err
	}
	out := &player.TierTable{Max: t.Max}
	for _, ti := range t.AllTiers {
		out.Tiers = append(out.Tiers, player.Tier{ID: ti.ID, Label: ti.Label, Rank: ti.Rank, Locked: ti.Locked})
	}
	return out, nil
}

func (a apiAdapter) Resolve(mid, mediaMid string, songType int64, tier string, deprioritize []string) (*player.StreamInfo, error) {
	r, err := a.c.ResolveStream(mid, mediaMid, int(songType), tier, true, deprioritize)
	if err != nil {
		return nil, err
	}
	return &player.StreamInfo{
		URL:       a.c.Base + r.Path,
		Tier:      r.Tier,
		TierLabel: r.TierLabel,
		Degraded:  r.Degraded,
		Size:      r.Size,
	}, nil
}

func (a apiAdapter) FetchLyric(mid string, trans bool) (string, string, error) {
	l, err := a.c.Lyric(mid, trans)
	if err != nil {
		return "", "", err
	}
	return l.Lyric, l.Trans, nil
}

func (a apiAdapter) LikeSong(songID, writeType int64, like bool) error {
	return a.c.SongLike(songID, writeType, like)
}
