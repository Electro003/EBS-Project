package routing

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

// RendezvousHash (Highest Random Weight - HRW)
func GetOwnerNode(key string, nodeAddresses []string) string {
	if len(nodeAddresses) == 0 {
		return ""
	}
	if len(nodeAddresses) == 1 {
		return nodeAddresses[0]
	}

	var maxWeight uint64
	var ownerNode string

	sort.Strings(nodeAddresses)

	for _, nodeAddr := range nodeAddresses {
		weight := calculateWeight(key, nodeAddr)
		if weight > maxWeight {
			maxWeight = weight
			ownerNode = nodeAddr
		}
	}
	return ownerNode
}

func calculateWeight(key, nodeAddr string) uint64 {
	hasher := sha256.New()
	hasher.Write([]byte(key))
	hasher.Write([]byte(nodeAddr))
	hash := hasher.Sum(nil)

	return binary.BigEndian.Uint64(hash[:8])
}
