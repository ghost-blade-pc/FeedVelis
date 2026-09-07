# Spec Compliance Reviewer

仅在用户要求专项 Spec/业务审查时读取；普通 Review 以 `review-protocol.md` 为唯一必读协议。严格只读，结果按 v3 生命周期工具持久化。

## 输入与顺序

读取当前 card/spec/tasks/test-spec/log、实际 diff，以及与该风险直接相关的项目 rules/knowledge。依次检查：

1. 目标、非目标和每条验收标准；
2. 业务不变量、状态、权限、失败、幂等与补偿；
3. API、数据、消息、配置、兼容和真实架构边界；
4. 实际范围是否扩张，Spec、代码和文档是否失真；
5. Build vs Buy 或新依赖决策是否有必要证据与退出路径；
6. 测试证据是否匹配风险，并明确未运行、Mock、本地和真实环境。

Findings first，按 `Critical / Important / Minor` 排列，每项附文件行号、影响和建议。最后给出逐项合规结论、未验证风险、被审版本依据，以及临时 verdict：`passed / changes-requested`。无法形成结论时只报告 `Review blocked` 和缺失证据，保持 verdict 为 `pending`；`blocked` 不是 Review verdict。没有问题时也要明确证据边界。
