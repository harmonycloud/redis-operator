package util

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"

	"k8s.io/apimachinery/pkg/util/rand"
	//hashutil "k8s.io/kubernetes/pkg/util/hash"
)

const (
	OperatorRevisionHashLabelKey = "middleware-operator-revision-hash"
)

// ComputeHash returns a hash value calculated from pod template and
// a collisionCount to avoid hash collision. The hash will be safe encoded to
// avoid bad words.
func ComputeHash(spec interface{}, collisionCount *int32) string {
	podTemplateSpecHasher := fnv.New32a()
	DeepHashObject(podTemplateSpecHasher, spec)

	// Add collisionCount in the hash if it exists.
	if collisionCount != nil {
		collisionCountBytes := make([]byte, 8)
		binary.LittleEndian.PutUint32(collisionCountBytes, uint32(*collisionCount))
		podTemplateSpecHasher.Write(collisionCountBytes)
	}

	return rand.SafeEncodeString(fmt.Sprint(podTemplateSpecHasher.Sum32()))
}
