package errcode

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

// err.server.file.* — modules/file business error codes (api.go). DefaultMessage
// holds the en-US source (D4); the zh-CN runtime translation lives in
// pkg/i18n/locales/active.zh-CN.toml. Internal=true codes never surface their
// message on the wire — callers MUST log the underlying err with full context
// (zap.Error) before responding.
var (
	// ---- validation (400) ----------------------------------------------------

	// ErrFileRequestInvalid is the catch-all for missing/malformed request input
	// (BindJSON failure "数据格式有误", empty image list, missing/invalid fileSize
	// param, empty path / "path参数不能为空", invalid file type, missing upload
	// path). The offending field is surfaced via Details when identifiable.
	ErrFileRequestInvalid = register(codes.Code{
		ID:             "err.server.file.request_invalid",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Invalid request.",
		SafeDetailKeys: []string{"field"},
	})
	// ErrFileImageCountExceeded covers the compose-image count cap ("图片数量不能大于9").
	ErrFileImageCountExceeded = register(codes.Code{
		ID:             "err.server.file.image_count_exceeded",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Too many images.",
		SafeDetailKeys: []string{"max"},
	})
	// ErrFileInvalidPath covers a rejected/unsafe upload-or-download path
	// (sanitizePath failure, "无效的文件路径") — appears across multiple handlers.
	ErrFileInvalidPath = register(codes.Code{
		ID:             "err.server.file.invalid_path",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Invalid file path.",
	})
	// ErrFileTooLarge surfaces the upload size cap (in MB) so the client can
	// render a localized hint without hard-coding the limit.
	ErrFileTooLarge = register(codes.Code{
		ID:             "err.server.file.too_large",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "The file exceeds the maximum allowed size.",
		SafeDetailKeys: []string{"max_mb"},
	})
	// ErrFileExtensionRequired covers an extension-less upload ("文件必须包含扩展名").
	ErrFileExtensionRequired = register(codes.Code{
		ID:             "err.server.file.extension_required",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "The file must have an extension.",
	})
	// ErrFileTypeUnsupported covers a blocked / unsupported upload type
	// ("禁止上传%s类型", "不支持上传%s类型", "不支持的文件类型"). The rejected
	// extension is surfaced via Details when known.
	ErrFileTypeUnsupported = register(codes.Code{
		ID:             "err.server.file.type_unsupported",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Unsupported file type.",
		SafeDetailKeys: []string{"ext"},
	})
	// ErrFileContentMismatch covers a magic-number / extension mismatch
	// ("文件内容与扩展名不匹配").
	ErrFileContentMismatch = register(codes.Code{
		ID:             "err.server.file.content_mismatch",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "The file content does not match its extension.",
	})

	// ---- internal (500, Internal=true) ---------------------------------------

	// ErrFileReadFailed covers a failure reading the uploaded multipart file /
	// its header. Log the underlying err before responding.
	ErrFileReadFailed = register(codes.Code{
		ID:             "err.server.file.read_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to read the file.",
		Internal:       true,
	})
	// ErrFileProcessFailed covers post-read processing failures (file-pointer
	// reset, signature copy). Log the underlying err before responding.
	ErrFileProcessFailed = register(codes.Code{
		ID:             "err.server.file.process_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to process the file.",
		Internal:       true,
	})
	// ErrFileImageComposeFailed covers the download-and-compose image path
	// ("组合图片失败", "图片合成返回结果异常"). Log the underlying err / context
	// before responding.
	ErrFileImageComposeFailed = register(codes.Code{
		ID:             "err.server.file.image_compose_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to compose images.",
		Internal:       true,
	})
	// ErrFileUploadFailed covers the storage upload path ("上传文件失败"). Log the
	// underlying err before responding.
	ErrFileUploadFailed = register(codes.Code{
		ID:             "err.server.file.upload_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to upload the file.",
		Internal:       true,
	})
	// ErrFilePresignFailed covers presigned PUT/GET URL generation failures. Log
	// the underlying err before responding.
	ErrFilePresignFailed = register(codes.Code{
		ID:             "err.server.file.presign_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to generate the presigned URL.",
		Internal:       true,
	})
)
