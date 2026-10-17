package infrastructure

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/nightnoryu/tesseract/internal/semantic/domain"
	"github.com/pelletier/go-toml/v2"
)

// Load reads only repository-managed catalog files. It never resolves secrets or connects to a DWH.
func Load(root fs.FS) (domain.Catalog, error) {
	var c domain.Catalog
	if err := decode(root, "sources.toml", &c); err != nil {
		return domain.Catalog{}, err
	}
	files, err := fs.Glob(root, "datasets/*.toml")
	if err != nil {
		return domain.Catalog{}, err
	}
	for _, path := range files {
		var file struct {
			Dataset    domain.Dataset     `toml:"dataset"`
			Columns    []domain.Column    `toml:"columns"`
			Dimensions []domain.Dimension `toml:"dimensions"`
			Measures   []domain.Measure   `toml:"measures"`
		}
		if err := decode(root, path, &file); err != nil {
			return domain.Catalog{}, err
		}
		file.Dataset.Columns = file.Columns
		file.Dataset.Dimensions = file.Dimensions
		file.Dataset.Measures = file.Measures
		c.Datasets = append(c.Datasets, file.Dataset)
	}
	if err := c.Validate(); err != nil {
		return domain.Catalog{}, err
	}
	return c, nil
}
func decode(root fs.FS, path string, dst any) error {
	data, err := fs.ReadFile(root, path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := toml.NewDecoder(strings.NewReader(string(data))).DisallowUnknownFields().Decode(dst); err != nil {
		// Decoder errors may contain TOML values; do not echo accidental inline secrets.
		return fmt.Errorf("%s: invalid TOML or unknown field", path)
	}
	return nil
}
