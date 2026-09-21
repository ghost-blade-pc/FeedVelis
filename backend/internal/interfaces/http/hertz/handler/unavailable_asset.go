package handler

import (
	"context"

	assetApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/asset"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	assetDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/asset"
)

// UnavailableAssets 是未配置对象存储时的降级实现：所有资产端点稳定返回
// 503 ASSET_UNAVAILABLE，而不是让路由消失。路由存在与否是装配契约的一部分，
// 客户端据此区分「这个部署没有图片能力」和「请求路径写错了」。
type UnavailableAssets struct{}

var _ AssetService = UnavailableAssets{}

func (UnavailableAssets) Create(context.Context, assetApp.CreateCommand) (assetApp.Upload, bool, error) {
	return assetApp.Upload{}, false, ports.ErrAssetStorageUnavailable
}

func (UnavailableAssets) Confirm(context.Context, assetApp.ConfirmCommand) (assetDomain.Asset, bool, error) {
	return assetDomain.Asset{}, false, ports.ErrAssetStorageUnavailable
}

func (UnavailableAssets) Open(context.Context, assetApp.ContentCommand) (assetApp.Content, error) {
	return assetApp.Content{}, ports.ErrAssetStorageUnavailable
}

func (UnavailableAssets) Stat(context.Context, assetApp.ContentCommand) (ports.ObjectInfo, error) {
	return ports.ObjectInfo{}, ports.ErrAssetStorageUnavailable
}
