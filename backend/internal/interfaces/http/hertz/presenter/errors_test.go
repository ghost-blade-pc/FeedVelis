package presenter

import (
	"errors"
	"fmt"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"

	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

func TestMapContentErrorCoversContractCodes(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"缺少幂等键":       {ErrIdempotencyKeyRequired, consts.StatusBadRequest, CodeIdempotencyKeyRequired},
		"非法幂等键":       {ErrIdempotencyKeyInvalid, consts.StatusBadRequest, CodeIdempotencyKeyInvalid},
		"幂等键复用":       {idempotencyApp.ErrKeyReused, consts.StatusConflict, CodeIdempotencyKeyReused},
		"幂等操作进行中":     {idempotencyApp.ErrPending, consts.StatusConflict, CodeIdempotencyInProgress},
		"缺少 If-Match": {ErrIfMatchRequired, consts.StatusBadRequest, CodeIfMatchRequired},
		"非法 If-Match": {ErrIfMatchInvalid, consts.StatusBadRequest, CodeIfMatchInvalid},
		"文章版本冲突":      {articleDomain.ErrVersionConflict, consts.StatusConflict, CodeArticleVersionConflict},
		"管理员锁定":       {articleDomain.ErrAdminOffline, consts.StatusForbidden, CodeArticleAdminOffline},
		"状态不允许":       {articleDomain.ErrInvalidState, consts.StatusConflict, CodeArticleInvalidState},
		"无权限":         {articleDomain.ErrForbidden, consts.StatusForbidden, CodeForbidden},
		"内容校验失败":      {ports.InvalidContent(errors.New("标题不能超过 200 个 Unicode 字符")), consts.StatusBadRequest, CodeArticleContentInvalid},
		"资产不可用":       {ports.ErrArticleAssetUnavailable, consts.StatusConflict, CodeArticleAssetInvalid},
		"文章不存在":       {articleDomain.ErrNotFound, consts.StatusNotFound, CodeArticleNotFound},
		"参数无效":        {articleDomain.ErrInvalidArgument, consts.StatusBadRequest, CodeValidationFailed},
		"正文格式错误":      {ErrMalformedBody, consts.StatusBadRequest, CodeValidationFailed},
		"依赖故障":        {errors.New("连接被拒绝"), consts.StatusServiceUnavailable, CodeDependencyUnavailable},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			mapping := MapContentError(testCase.err)
			if mapping.Status != testCase.status || mapping.Code != testCase.code {
				t.Fatalf("status = %d code = %s，期望 %d/%s", mapping.Status, mapping.Code, testCase.status, testCase.code)
			}
		})
	}
}

func TestMapContentErrorKeepsRendererReason(t *testing.T) {
	reason := errors.New("Markdown 不能超过 256 KiB")
	marked := ports.InvalidContent(reason)

	mapping := MapContentError(marked)
	if mapping.Code != CodeArticleContentInvalid || mapping.Message != reason.Error() {
		t.Fatalf("code = %s message = %q", mapping.Code, mapping.Message)
	}
}

func TestMapContentErrorSurvivesWrapAndKeepsReasonIdentity(t *testing.T) {
	reason := errors.New("图片必须使用 asset:<uuid> 引用")
	wrapped := fmt.Errorf("保存草稿: %w", ports.InvalidContent(reason))

	if mapping := MapContentError(wrapped); mapping.Code != CodeArticleContentInvalid {
		t.Fatalf("code = %s", mapping.Code)
	}
	if !errors.Is(wrapped, ports.ErrContentInvalid) || !errors.Is(wrapped, reason) {
		t.Fatalf("包装后丢失错误标识: %v", wrapped)
	}
}
