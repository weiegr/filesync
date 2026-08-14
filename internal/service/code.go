package service

import (
	"crypto/rand"
	"fmt"
	"math/big"

	"filesync/internal/store"
)

// CodeGenerator 6 位数字邀请码生成器
type CodeGenerator struct {
	shareStore *store.ShareStore
}

func NewCodeGenerator(ss *store.ShareStore) *CodeGenerator {
	return &CodeGenerator{shareStore: ss}
}

// Generate 生成一个未占用的 6 位邀请码，最多重试 10 次
func (g *CodeGenerator) Generate() (string, error) {
	for i := 0; i < 10; i++ {
		code, err := randomCode()
		if err != nil {
			return "", err
		}
		exists, err := g.shareStore.CodeExists(code)
		if err != nil {
			return "", err
		}
		if !exists {
			return code, nil
		}
	}
	return "", fmt.Errorf("生成邀请码失败：重试次数耗尽")
}

// randomCode 用 crypto/rand 生成 6 位数字
func randomCode() (string, error) {
	max := big.NewInt(1_000_000) // 0 - 999999
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
