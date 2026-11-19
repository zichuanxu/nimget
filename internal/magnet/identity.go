package magnet

import (
	"errors"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	infohashv2 "github.com/anacrolix/torrent/types/infohash-v2"
)

var errIdentityMismatch = errors.New("torrent metadata does not match the magnet link's info hashes")

func identityOptions(identity download.TorrentIdentity) (torrent.AddTorrentOpts, []metainfo.Hash, error) {
	opts := torrent.AddTorrentOpts{DeferStorageOpen: true, DisableInitialPieceCheck: true}
	var announceHashes []metainfo.Hash
	if identity.V1 != "" {
		if err := opts.InfoHash.FromHexString(identity.V1); err != nil || opts.InfoHash.IsZero() {
			return opts, nil, errors.New("saved BitTorrent v1 info hash is invalid")
		}
		announceHashes = append(announceHashes, opts.InfoHash)
	}
	if identity.V2 != "" {
		var hash infohashv2.T
		if err := hash.FromHexString(identity.V2); err != nil || hash == (infohashv2.T{}) {
			return opts, nil, errors.New("saved BitTorrent v2 info hash is invalid")
		}
		opts.InfoHashV2.Set(hash)
		if short := *hash.ToShort(); short != opts.InfoHash || len(announceHashes) == 0 {
			announceHashes = append(announceHashes, short)
		}
	}
	if len(announceHashes) == 0 {
		return opts, nil, errors.New("saved torrent identity is missing")
	}
	return opts, announceHashes, nil
}

// VerifyIdentity checks every supplied full hash against the original info
// dictionary and returns the aliases proven by the metadata.
func VerifyIdentity(claim download.TorrentIdentity, info *metainfo.Info, meta metainfo.MetaInfo) (download.TorrentIdentity, error) {
	var verified download.TorrentIdentity
	if info.HasV1() {
		verified.V1 = meta.HashInfoBytes().HexString()
	}
	if info.HasV2() {
		hash := infohashv2.HashBytes(meta.InfoBytes)
		verified.V2 = hash.HexString()
	}
	if !verified.Contains(claim) {
		return download.TorrentIdentity{}, errIdentityMismatch
	}
	return verified, nil
}
