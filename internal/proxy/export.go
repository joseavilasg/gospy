package proxy

// DecompressBody decompresses raw body bytes using magic-byte detection.
// Exported for use by the webui package for lazy decompression on detail API.
func DecompressBody(data []byte, contentEncoding string) string {
	return decompressBody(data, contentEncoding).Decoded
}
