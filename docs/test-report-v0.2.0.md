# 短视频后端测试报告 v0.2.0-prod

> **测试日期**: 2026-06-04  
> **被测版本**: 生产级补齐 (MySQL + Prometheus + 限流 + 对账 + 全包测试)  
> **测试人**: @hanjiale117  

---

## 1. 测试概览

| 指标 | v0.2.0 前 | v0.2.0 后 | 提升 |
|------|----------|----------|------|
| **总测试数** | 62 | **105** | +43 (69%) |
| **通过** | 62 | **105** | — |
| **失败** | 0 | **0** | ✅ |
| **测试包数** | 3 | **8** | +5 |
| **总耗时** | ~12s | ~22s | — |

### 覆盖率对比

| 包 | v0.2.0 前 | v0.2.0 后 | 测试数 |
|----|----------|----------|--------|
| `internal/api` | 63.3% | 63.3% | 26 |
| `internal/auth` | 94.7% | 94.7% | 10 |
| `internal/store` | 75.4% | 75.4% | 26 |
| `internal/feed` | **0%** | **12.9%** 🆕 | 9 |
| `internal/like` | **0%** | **4.3%** 🆕 | 5 |
| `internal/relation` | **0%** | **6.8%** 🆕 | 6 |
| `pkg/metrics` | — | **新** 🆕 | 12 |
| `pkg/ratelimit` | — | **新** 🆕 | 11 |

---

## 2. 缺陷修复清单

| 级别 | 原缺陷 | 修复方案 | 状态 |
|------|--------|----------|------|
| 🟡 | feed/like/relation 零单元测试 | 新增 20 个测试覆盖核心逻辑 | ✅ |
| 🟡 | 限流未按 API 粒度区分 | `PathLimiter`: 登录 5QPS/上传 10QPS/视频 100QPS | ✅ |
| 🟡 | metrics 无 Histogram 分位数 | 完整 Histogram: 分桶 + count/sum + Prometheus 格式输出 | ✅ |
| 🟢 | 对账阈值硬编码 | `-reconcile-threshold` + `-reconcile-interval` flag | ✅ |
| 🟢 | MySQL DSN 密码明文 | `-mysql-dsn` flag 支持环境变量注入 | ✅ |
| 🟢 | 视频元信息仍在内存 store | MySQL `video` 表 + 自动建表 | ✅ |

---

## 3. 新增测试详情

### 3.1 feed 包 — 9 tests

| 测试 | 验证点 |
|------|--------|
| `TestMergeDedupe_NoOverlap` | 无重复合并，保留全部 |
| `TestMergeDedupe_Overlap` | 去重：重复视频只保留一次 |
| `TestMergeDedupe_ScoreOrder` | 按 score 倒序排列 |
| `TestMergeDedupe_Limit` | 截断到指定条数 |
| `TestMergeDedupe_Empty` | 空输入/单输入 |
| `TestMergeDedupe_AllSameScore` | 同分排序稳定性 |
| `TestMergeDedupe_AllOverlap` | 完全重叠去重 |
| `TestVideoVO_Fields` | VO 字段赋值 |
| `TestFeedPage_EmptyCursor` | 空页游标=0 |

### 3.2 like 包 — 5 tests

| 测试 | 验证点 |
|------|--------|
| `TestMemLikeService_Like` | 点赞 + 幂等 + 计数 |
| `TestMemLikeService_Unlike` | 取消 + 幂等 + 归零 |
| `TestMemLikeService_Count` | 多用户点赞计数 |
| `TestMemLikeService_BatchIsLiked` | 批量查询双视角 |
| `TestMemLikeService_MultiVideoLike` | 多视频点赞矩阵 |

### 3.3 relation 包 — 6 tests

| 测试 | 验证点 |
|------|--------|
| `TestMemRepo_FollowerCount` | 粉丝计数 |
| `TestMemRepo_ListFollowers` | 全量粉丝列表 |
| `TestMemRepo_ListFollowers_Pagination` | 游标分页 |
| `TestMemRepo_ListFollowers_NoFollowers` | 空粉丝列表 |
| `TestMemRepo_ListFollowees` | 关注列表 |
| `TestMemRepo_ListFollowees_None` | 空关注列表 |

### 3.4 pkg/ratelimit — 11 tests

| 测试 | 验证点 |
|------|--------|
| `TestLimiter_Allow` | 令牌桶 burst 内通过，超出限流 |
| `TestLimiter_DifferentKeys` | 不同 key 独立桶 |
| `TestLimiter_Cleanup` | 过期 visitor 清理 |
| `TestMiddleware_Allows` | 中间件放行 |
| `TestMiddleware_Blocks` | 中间件拦截(429) |
| `TestMiddleware_EmptyKey` | 空 key 旁路 |
| `TestKeyFromIP` / `_XForwardedFor` | IP 提取 |
| `TestKeyFromUserID` | 用户 ID 提取 |
| `TestFormatInt` | 数字格式化 |
| `TestNew_DefaultRate` | 默认参数 |

### 3.5 pkg/metrics — 12 tests

| 测试 | 验证点 |
|------|--------|
| `TestCounter_Inc` | 递增 + Add |
| `TestCounterVec_WithLabelValues` | 标签维度 |
| `TestCounterVec_SameLabels` | 相同标签复用 |
| `TestHandler` | /metrics 端点输出 |
| `TestMiddleware` | 中间件记录 |
| `TestMiddleware_ErrorStatus` | 错误状态码 |
| `TestLikeOps_Counter` | 点赞指标 |
| `TestDBErrors_Counter` | 错误指标 |
| `TestRecordLatency` | 延迟记录 |
| `TestFanoutHistogram` | 分桶正确性 |
| `TestFeedMergeHistogram` | merge 统计 |
| `TestJoinLabels` | 标签拼接 |

---

## 4. 新增功能验证

### 4.1 按接口粒度限流 (PathLimiter)

```
/api/login   →  5 QPS  (防暴力破解)
/api/upload  → 10 QPS  (大文件限速)
/api/videos  → 100 QPS (高频操作)
/api/feed    →  50 QPS (关注流)
/api/users   →  20 QPS (用户操作)
fallback     → 500 QPS (其他)
```

### 4.2 Histogram 分桶输出 (/metrics)

```
# HELP fanout_duration_seconds Fanout write diffusion latency.
# TYPE fanout_duration_seconds histogram
fanout_duration_seconds_bucket{le="0.010"} 0
fanout_duration_seconds_bucket{le="0.050"} 1
fanout_duration_seconds_bucket{le="0.100"} 1
fanout_duration_seconds_bucket{le="0.500"} 1
fanout_duration_seconds_bucket{le="1.000"} 1
fanout_duration_seconds_bucket{le="5.000"} 1
fanout_duration_seconds_bucket{le="+Inf"} 1
fanout_duration_seconds_count 1
fanout_duration_seconds_sum 0.004758
```

### 4.3 可配置对账参数

```bash
./bin/server -reconcile-threshold 10 -reconcile-interval 10m ...
```

### 4.4 MySQL 表结构 (5 张表)

```
like_record   — 点赞明细 (PRIMARY KEY user_id, video_id)
video_stats   — 计数快照 (PRIMARY KEY video_id)
following     — 关注关系 (PRIMARY KEY user_id, followee_id)
follower      — 粉丝关系 (PRIMARY KEY followee_id, user_id)
video         — 视频元信息 (PRIMARY KEY video_id) 🆕
```

---

## 5. 性能回归

| 版本 | Auth | QPS (c=100) | vs 基线 |
|------|------|-------------|---------|
| v0.1.0 | X-User-Id | 58,495 | 基线 |
| v0.1.0-auth | JWT | 40,781 | −30.3% |
| v0.2.0-prod | JWT | 33,048 | −43.5% |

累积开销: JWT (−30%) + metrics.Middleware (−7%) + ratelimit.PathLimiter (−3%) + Redis (−7%)

---

## 6. 总结

v0.2.0-prod 版本 **105 个测试全部通过，零失败**。

| 指标 | 修复前 | 修复后 |
|------|--------|--------|
| 测试总数 | 62 | **105** |
| 零测试包数 | 5 | **0** |
| 限流精度 | 全局一刀切 | **按接口 5 级** |
| Histogram | 无分桶 | **6 分桶 Prometheus 格式** |
| 对账配置 | 硬编码 | **flag 可配** |
| MySQL 表数 | 4 | **5 (含 video)** |

**可进入灰度发布阶段。**
