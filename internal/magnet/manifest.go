package magnet

import (
	"errors"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// Manifest maps torrent byte ranges to output files. Synthetic padding has
// no output file, and v2 alignment gaps have no extent at all.
type Manifest struct {
	Files     []PayloadFile
	Directory bool
	Name      string
	Total     int64
	info      *metainfo.Info
	extents   []int
	v1Size    int64
}

type PayloadFile struct {
	Path   []string
	Size   int64
	Offset int64
	Pad    bool
	meta   metainfo.FileInfo
	before int64
}

func readManifest(info *metainfo.Info) (Manifest, error) {
	invalid := errors.New("torrent metadata has an invalid file layout")
	m := Manifest{info: info, Name: info.BestName()}
	if info.PieceLength <= 0 || info.SymlinkPath != nil || strings.ContainsAny(info.Attr, "lp") {
		return Manifest{}, invalid
	}
	var payloads int
	var end int64
	for file := range info.UpvertedFilesIter() {
		pad := strings.ContainsRune(file.Attr, 'p')
		if file.SymlinkPath != nil || strings.ContainsRune(file.Attr, 'l') || file.Length < 0 ||
			file.TorrentOffset < end || file.TorrentOffset > math.MaxInt64-file.Length {
			return Manifest{}, invalid
		}
		if !pad {
			payloads++
		}
		if file.Length > 0 {
			m.extents = append(m.extents, len(m.Files))
		}
		m.Files = append(m.Files, PayloadFile{
			Path: file.BestPath(), Size: file.Length, Offset: file.TorrentOffset, Pad: pad, meta: file, before: m.Total,
		})
		if !pad {
			m.Total += file.Length
		}
		end = file.TorrentOffset + file.Length
	}
	if payloads == 0 || end > math.MaxInt64-(info.PieceLength-1) || info.HasV2() && (len(m.Files[0].Path) == 0 || !info.FileTree.IsDir()) {
		return Manifest{}, invalid
	}
	if info.HasV1() {
		m.v1Size = end
		if info.HasV2() {
			size, ok := m.hybridV1Size()
			if !ok {
				return Manifest{}, errors.New("hybrid torrent file layouts disagree")
			}
			m.v1Size = size
		}
		pieces := (m.v1Size + info.PieceLength - 1) / info.PieceLength
		if len(info.Pieces)%metainfo.HashSize != 0 || int64(len(info.Pieces)/metainfo.HashSize) != pieces ||
			pieces != (end+info.PieceLength-1)/info.PieceLength {
			return Manifest{}, invalid
		}
	}
	m.Directory = len(m.Files) > 1
	if !m.Directory && len(m.Files[0].Path) != 0 {
		m.Name = m.Files[0].Path[len(m.Files[0].Path)-1]
	}
	return m, nil
}

func (m Manifest) hybridV1Size() (int64, bool) {
	var size int64
	var index int
	for file := range m.info.UpvertedV1Files() {
		if file.Length < 0 || file.TorrentOffset != size || file.Length > math.MaxInt64-size ||
			file.SymlinkPath != nil || strings.ContainsRune(file.Attr, 'l') {
			return 0, false
		}
		size += file.Length
		if strings.ContainsRune(file.Attr, 'p') {
			continue
		}
		if index >= len(m.Files) {
			return 0, false
		}
		path := file.BestPath()
		if !m.info.IsDir() || len(m.info.Files) == 0 {
			path = []string{m.info.BestName()}
		}
		entry := m.Files[index]
		if !slices.Equal(path, entry.Path) || file.TorrentOffset != entry.Offset || file.Length != entry.Size {
			return 0, false
		}
		index++
	}
	return size, index == len(m.Files) && size <= math.MaxInt64-(m.info.PieceLength-1)
}

// The engine's FileTreeFile omits BEP 47 attributes and cannot distinguish a
// file with empty properties from a directory carrying file properties.
func validateManifestEncoding(info *metainfo.Info, raw []byte) error {
	var fields struct {
		Length bencode.Bytes `bencode:"length"`
		Files  bencode.Bytes `bencode:"files"`
		Tree   bencode.Bytes `bencode:"file tree"`
	}
	invalid := errors.New("torrent metadata has an invalid or unsafe file layout")
	if err := bencode.Unmarshal(raw, &fields); err != nil {
		return invalid
	}
	if info.HasV1() && ((fields.Length == nil) == (fields.Files == nil) || fields.Files != nil && len(info.Files) == 0) {
		return invalid
	}
	if info.HasV2() && !regularFileTree(fields.Tree, true) {
		return invalid
	}
	return nil
}

func regularFileTree(raw []byte, root bool) bool {
	var children map[string]bencode.Bytes
	if err := bencode.Unmarshal(raw, &children); err != nil || len(children) == 0 {
		return false
	}
	if file, ok := children[""]; ok {
		var properties struct {
			Length bencode.Bytes `bencode:"length"`
			metainfo.ExtendedFileAttrs
		}
		return !root && len(children) == 1 && bencode.Unmarshal(file, &properties) == nil &&
			properties.Length != nil && properties.SymlinkPath == nil && !strings.ContainsAny(properties.Attr, "lp")
	}
	for _, child := range children {
		if !regularFileTree(child, false) {
			return false
		}
	}
	return true
}

func (m Manifest) extentAt(off int64) int {
	return sort.Search(len(m.extents), func(i int) bool {
		f := m.Files[m.extents[i]]
		return f.Offset+f.Size > off
	})
}

func (m Manifest) payloadBefore(off int64) int64 {
	index := m.extentAt(off)
	if index == len(m.extents) {
		return m.Total
	}
	f := m.Files[m.extents[index]]
	if f.Pad {
		return f.before
	}
	return f.before + max(off-f.Offset, 0)
}
