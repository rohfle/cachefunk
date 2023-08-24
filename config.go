package cachefunk

import (
	"bytes"
	"compress/gzip"
	"io"
)

var DEFAULT_KEYCONFIG = &KeyConfig{
	TTL:            3600,
	TTLJitter:      300,
	UseCompression: true,
}

type CacheFunkConfig struct {
	Defaults *KeyConfig
	Configs  map[string]*KeyConfig
}

func (c *CacheFunkConfig) Get(key string) *KeyConfig {
	if value, exists := c.Configs[key]; exists {
		return value
	} else if c.Defaults != nil {
		c.Configs[key] = c.Defaults
		return c.Defaults
	} else {
		return DEFAULT_KEYCONFIG
	}
}

// Config is used to configure the caching wrapper functions
type KeyConfig struct {
	// TTL is time to live in seconds before the cache value can be deleted
	// If TTL is 0, cache value will expire immediately
	// Use a very large TTL to make the cached value last a long time
	// (for instance 31536000 will cache for one year)
	TTL int64
	// When TTLJitter is > 0, a random value from 1 to TTLJitter will be added to TTL
	// This spreads cache expiry out to stop getting fresh responses all at once
	TTLJitter int64
	// Enable compression of data by gzip
	UseCompression bool
}

func compressBytes(input []byte) ([]byte, error) {
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	writer.Write(input)
	err := writer.Close()
	if err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func decompressBytes(input []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(input))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(reader)
}
