# K8s 配置文档索引

## 核心文档

### 快速开始
- [README.md](../README.md) - 快速开始指南

### 架构文档
- [架构设计](ARCHITECTURE.md) - 系统架构和设计原则（待创建）

### 部署文档
- [部署指南](DEPLOYMENT.md) - 详细部署说明（待创建）

## 历史文档（过程性文档）

以下文档记录了架构改进和重构的过程，保留用于参考：

### 架构改进
- [ARCHITECTURE_COMPARISON.md](history/ARCHITECTURE_COMPARISON.md) - 架构改进前后对比
- [ARCHITECTURE_IMPROVEMENTS.md](history/ARCHITECTURE_IMPROVEMENTS.md) - 架构改进方案

### 重构记录
- [REFACTORING_NOTES.md](history/REFACTORING_NOTES.md) - 重构说明
- [REFACTORING_SUMMARY.md](history/REFACTORING_SUMMARY.md) - 重构总结

### 清理记录
- [CLEANUP_SUMMARY.md](history/CLEANUP_SUMMARY.md) - 代码库清理总结
- [FINAL_REVIEW.md](history/FINAL_REVIEW.md) - 最终审查报告
- [BEST_PRACTICES_CHECK.md](history/BEST_PRACTICES_CHECK.md) - 最佳实践检查清单

### GC 相关
- [GC_DEPLOYMENT_SUCCESS.md](history/GC_DEPLOYMENT_SUCCESS.md) - GC 部署成功记录
- [GC_FIX_SUMMARY.md](history/GC_FIX_SUMMARY.md) - GC 修复总结
- [GC_ISSUE_ANALYSIS.md](history/GC_ISSUE_ANALYSIS.md) - GC 问题分析
- [GC_TTL_ANALYSIS.md](history/GC_TTL_ANALYSIS.md) - GC TTL 分析

### 代码质量
- [CODE_QUALITY_ASSESSMENT.md](history/CODE_QUALITY_ASSESSMENT.md) - 代码质量评估报告

## 目录结构

```
k8s/
├── README.md                    # 主要文档
├── docs/
│   ├── INDEX.md                 # 本文档（文档索引）
│   └── history/                 # 过程性文档
│       ├── ARCHITECTURE_*.md
│       ├── REFACTORING_*.md
│       ├── CLEANUP_*.md
│       └── ...
├── base/                        # Kustomize base 配置
├── overlays/                    # 环境特定配置
└── scripts/                     # 部署和管理脚本
```
