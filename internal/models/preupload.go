package models

import "strings"

// PreUploadReq is the request body for POST /api/localsend/v2/prepare-upload
// Per protocol spec Section 4.1, info must include port and protocol.
type PreUploadReq struct {
	Info  *SenderInfo `json:"info"`
	Files FileMetas   `json:"files"`
}

// PreDownloadResp is the response body for POST /api/localsend/v2/prepare-download
// Per protocol spec Section 5.2, info does NOT include port and protocol.
type PreDownloadResp struct {
	Info      *DeviceInfo `json:"info"`
	SessionId string      `json:"sessionId"`
	Files     FileMetas   `json:"files"`
}

type FileMetas map[string]FileMeta

// IsFolderTransfer checks if any file in the map has a path separator,
// indicating this is a folder/directory transfer.
func (fm FileMetas) IsFolderTransfer() bool {
	for _, meta := range fm {
		if strings.Contains(meta.Filename, "/") {
			return true
		}
	}
	return false
}

// IsMessage reports whether the files are a text message: a single file whose
// content travels in its preview, so the receiver may request no upload.
func (fm FileMetas) IsMessage() bool {
	if len(fm) != 1 {
		return false
	}
	for _, meta := range fm {
		return meta.Preview != ""
	}
	return false
}

type PreUploadResp struct {
	SessionId string     `json:"sessionId"`
	Tokens    FileTokens `json:"files"`
}

type FileTokens map[string]string
