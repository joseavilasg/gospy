package proxy

// DecompressBody decompresses raw body bytes using magic-byte detection.
// Exported for use by the webui package for lazy decompression.
func DecompressBody(data []byte, contentEncoding string) DecompressResult {
	return decompressBody(data, contentEncoding)
}
