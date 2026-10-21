package update

import "testing"

func TestMIPSBundleSelectionSeparatesByteOrder(t *testing.T) {
	for _, arch := range []string{"mips", "mipsle"} {
		f := newStagingFixture(t)
		for i := range f.manifest.Assets {
			f.manifest.Assets[i].OS = "linux"
			f.manifest.Assets[i].Arch = arch
			f.manifest.Assets[i].GOARM = ""
		}
		if _, err := bundleAssetsFor(f.manifest, releasePlatform{"linux", arch, ""}); err != nil {
			t.Fatal(err)
		}
		other := "mips"
		if arch == "mips" {
			other = "mipsle"
		}
		if _, err := bundleAssetsFor(f.manifest, releasePlatform{"linux", other, ""}); err == nil {
			t.Fatal("selected opposite-endian update bundle")
		}
	}
}
