package service

import "github.com/hujinrun/flowspace/internal/model"

type notionGateway interface {
	TestDataSource(config notionTargetConfig) error
	QueryDataSource(dataSourceID string) ([]notionPage, error)
	RetrievePageBlocks(pageID string) ([]notionBlock, error)
	CreatePage(config notionTargetConfig, note *model.Note, blocks []notionBlock) (notionPage, error)
	UpdatePage(config notionTargetConfig, pageID string, note *model.Note, blocks []notionBlock) (notionPage, error)
	RestorePage(pageID string) error
}
