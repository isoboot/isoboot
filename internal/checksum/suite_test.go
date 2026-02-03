package checksum

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestChecksum(t *testing.T) {
	RegisterFailHandler(Fail)
	suiteConfig, reporterConfig := GinkgoConfiguration()
	reporterConfig.Verbose = true
	RunSpecs(t, "Checksum Suite", suiteConfig, reporterConfig)
}
