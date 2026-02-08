package validation

var allowedContentTypes = map[string]bool{
    "application/x-gzip": true,
    "application/gzip":    true,
    "application/x-tar":   true,
    "application/tar":     true,
}

func ValidateUploadContentType(contentType string) bool {
    return allowedContentTypes[contentType]
}