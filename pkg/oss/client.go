// Package oss 提供阿里云 OSS 上传能力，不可用时自动降级为本地文件存储。
package oss

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	aliyun "github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// Client 封装 OSS 操作。nil 时降级为本地文件复制。
type Client struct {
	bucket    *aliyun.Bucket
	localDir  string // fallback 本地目录
	cdnDomain string
}

// Config OSS 配置。Endpoint/Key 为空时使用本地模式。
type Config struct {
	Endpoint        string
	AccessKeyID     string
	AccessKeySecret string
	BucketName      string
	CDNDomain       string
	LocalDir        string // fallback: 本地存储路径
}

// New 创建 OSS 客户端，凭证为空时降级为本地存储。
func New(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" || cfg.AccessKeyID == "" {
		// 本地模式
		if cfg.LocalDir == "" {
			cfg.LocalDir = "./data/uploads"
		}
		if err := os.MkdirAll(cfg.LocalDir, 0755); err != nil {
			return nil, err
		}
		return &Client{localDir: cfg.LocalDir, cdnDomain: cfg.CDNDomain}, nil
	}
	client, err := aliyun.New(cfg.Endpoint, cfg.AccessKeyID, cfg.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("oss client: %w", err)
	}
	bucket, err := client.Bucket(cfg.BucketName)
	if err != nil {
		return nil, fmt.Errorf("oss bucket: %w", err)
	}
	return &Client{bucket: bucket, localDir: cfg.LocalDir, cdnDomain: cfg.CDNDomain}, nil
}

// PutObject 上传文件到 OSS（或复制到本地目录）。
// objectKey 如 "videos/123_720p.mp4"。
func (c *Client) PutObject(objectKey, filePath string) error {
	if c.bucket != nil {
		return c.bucket.PutObjectFromFile(objectKey, filePath)
	}
	// 本地模式: 复制文件
	dst := filepath.Join(c.localDir, objectKey)
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return copyFile(filePath, dst)
}

// PutObjectFromReader 从 Reader 上传文件。
func (c *Client) PutObjectFromReader(objectKey string, reader io.Reader) error {
	if c.bucket != nil {
		return c.bucket.PutObject(objectKey, reader)
	}
	dst := filepath.Join(c.localDir, objectKey)
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, reader)
	return err
}

// ObjectURL 返回对象的公开访问 URL。
func (c *Client) ObjectURL(objectKey string) string {
	if c.cdnDomain != "" {
		return strings.TrimRight(c.cdnDomain, "/") + "/" + strings.TrimLeft(objectKey, "/")
	}
	if c.bucket != nil {
		// CDN 域名优先（可配置）
		return fmt.Sprintf("https://%s.%s/%s", c.bucket.BucketName, c.bucket.Client.Config.Endpoint, objectKey)
	}
	return "/uploads/" + objectKey
}

// Health performs a lightweight readiness check.
func (c *Client) Health(ctx context.Context) error {
	if c.bucket != nil {
		done := make(chan error, 1)
		go func() {
			_, err := c.bucket.ListObjects(aliyun.MaxKeys(1))
			done <- err
		}()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			return err
		}
	}
	if c.localDir == "" {
		return fmt.Errorf("local storage dir is empty")
	}
	return os.MkdirAll(c.localDir, 0755)
}

// IsLocal 是否本地模式。
func (c *Client) IsLocal() bool { return c.bucket == nil }

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()
	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()
	_, err = io.Copy(d, s)
	return err
}
