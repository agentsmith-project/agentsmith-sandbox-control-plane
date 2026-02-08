package validation

import "testing"

func TestValidateUploadContentType(t *testing.T) {
    tests := []struct {
        name        string
        contentType string
        want        bool
    }{
        {
            name:        "gzip with x- prefix",
            contentType: "application/x-gzip",
            want:        true,
        },
        {
            name:        "gzip without x- prefix",
            contentType: "application/gzip",
            want:        true,
        },
        {
            name:        "tar with x- prefix",
            contentType: "application/x-tar",
            want:        true,
        },
        {
            name:        "tar without x- prefix",
            contentType: "application/tar",
            want:        true,
        },
        {
            name:        "unsupported content type",
            contentType: "application/octet-stream",
            want:        false,
        },
        {
            name:        "text/plain",
            contentType: "text/plain",
            want:        false,
        },
        {
            name:        "application/json",
            contentType: "application/json",
            want:        false,
        },
        {
            name:        "empty content type",
            contentType: "",
            want:        false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := ValidateUploadContentType(tt.contentType)
            if got != tt.want {
                t.Errorf("ValidateUploadContentType(%q) = %v, want %v", tt.contentType, got, tt.want)
            }
        })
    }
}