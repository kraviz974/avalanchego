package simplex

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"simplex"
	"sync"
)

var _ simplex.VerifiedBlock = (*VerifiedBlock)(nil)

type VerifiedBlock struct {
	computeDigestOnce sync.Once
	digest            simplex.Digest // cached, not serialized

	metadata   simplex.ProtocolMetadata
	innerBlock []byte
	accept     func(context.Context) error
}

// BlockHeader returns the block header for the verified block.
func (v *VerifiedBlock) BlockHeader() simplex.BlockHeader {
	v.computeDigestOnce.Do(v.computeDigest)
	return simplex.BlockHeader{
		ProtocolMetadata: v.metadata,
		Digest:           v.digest,
	}
}

func (v *VerifiedBlock) Bytes() []byte {
	mdBytes := v.metadataBytes()
	buff := make([]byte, len(mdBytes)+len(v.innerBlock))
	copy(buff, mdBytes)
	copy(buff[len(mdBytes):], v.innerBlock)
	return buff
}

// computeDigest computes the digest of the block.
func (v *VerifiedBlock) computeDigest() {
	mdBytes := v.metadataBytes()
	h := sha256.New()
	h.Write(v.innerBlock)
	h.Write(mdBytes)
	digest := h.Sum(nil)
	v.digest = simplex.Digest(digest[:])
}

func (v *VerifiedBlock) metadataBytes() []byte {
	buff := make([]byte, simplex.ProtocolMetadataLen)
	var pos int

	buff[pos] = v.metadata.Version
	pos++

	binary.BigEndian.PutUint64(buff[pos:], v.metadata.Epoch)
	pos += 8

	binary.BigEndian.PutUint64(buff[pos:], v.metadata.Round)
	pos += 8

	binary.BigEndian.PutUint64(buff[pos:], v.metadata.Seq)
	pos += 8

	copy(buff[pos:], v.metadata.Prev[:])
	return buff
}
