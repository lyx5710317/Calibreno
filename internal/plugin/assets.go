package plugin

import (
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
)

// maxAssetBytes also applies after gzip decoding.
const maxAssetBytes = 64 << 20

func (p *Plugin) hostAsset(r *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
	name, err := r.ToString(moejs.Arg(args, 0))
	if err != nil {
		return moejs.Undefined(), err
	}
	file, err := p.openAsset(name.GoString())
	if err != nil {
		return moejs.Undefined(), err
	}
	defer file.Close()
	var src io.Reader = file
	if len(args) > 1 {
		mode, err := r.ToString(args[1])
		if err != nil {
			return moejs.Undefined(), err
		}
		if mode.GoString() != "gzip" {
			return moejs.Undefined(), fmt.Errorf("assets.read accepts only gzip as compression")
		}
		gz, err := gzip.NewReader(file)
		if err != nil {
			return moejs.Undefined(), err
		}
		defer gz.Close()
		src = gz
	}
	data, err := io.ReadAll(io.LimitReader(src, maxAssetBytes+1))
	if err != nil {
		return moejs.Undefined(), err
	}
	if len(data) > maxAssetBytes {
		return moejs.Undefined(), fmt.Errorf("asset is over %d MB", maxAssetBytes>>20)
	}
	buffer, err := r.NewArrayBuffer(data)
	if err != nil {
		return moejs.Undefined(), err
	}
	return engine.ObjectValue(buffer), nil
}

// openAsset reads from this plugin's assets and fences off symlinks in
// linked plugins, whose folders are allowed to contain user files.
func (p *Plugin) openAsset(name string) (fs.File, error) {
	if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\:") {
		return nil, fmt.Errorf("invalid plugin asset path %q", name)
	}
	if p.Dir == "" {
		return p.fsys.Open("assets/" + name)
	}
	root, err := os.OpenRoot(p.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	assets, err := root.OpenRoot("assets")
	if err != nil {
		return nil, err
	}
	defer assets.Close()
	st, err := assets.Stat(name)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("plugin asset %q is not a regular file", name)
	}
	return assets.Open(name)
}
