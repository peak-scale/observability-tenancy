package utils

import (
	"github.com/prometheus/prometheus/prompb"
)

// Remove correct index from labels.
func RemoveOrdered(slice []prompb.Label, s int) []prompb.Label {
	return append(slice[:s], slice[s+1:]...)
}
