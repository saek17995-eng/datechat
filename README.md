# DateChat · 陌生人语音视频社交 App

Flutter + Go + MySQL + Redis，消息走腾讯 IM，音视频走腾讯 TRTC。

## 仓库结构

```
.
├── server/                 Go 后端（单体优先，按领域分包，可平移微服务）
│   ├── cmd/api             HTTP 服务入口
│   ├── cmd/migrate         数据库迁移工具（自研，无外部依赖）
│   ├── internal/config     配置加载（yaml + 环境变量展开）
│   ├── internal/bootstrap  logger / mysql / redis 初始化与重试
│   ├── internal/middleware trace / accesslog / recover / cors / ratelimit / jwt
│   ├── internal/router     路由注册
│   ├── internal/modules    业务领域（health / account / realname / profile / chat）
│   ├── internal/pkg        通用基础包（errs/resp/page/jwt/clock/ctxkey/crypto/phone/idcard/tlsig/tc3/sms/cos/textfilter）
│   ├── migrations          SQL 迁移脚本
│   └── configs             分环境配置
└── app/                    Flutter 客户端
    ├── lib/core            环境/主题/路由/网络/存储/DI
    ├── lib/features        业务页面（P0 为 4 个 Tab 骨架）
    └── lib/shared          通用组件
```

## 在线仓库与 Android 安装包

- 仓库：<https://github.com/saek17995-eng/datechat>
- 安装包：Release 里的 `datechat-*.apk`（Android 7.0 / API 24 以上，arm64，19MB）
  → <https://github.com/saek17995-eng/datechat/releases/tag/v0.1.0>

安装包由 GitHub Actions 自动构建：`.github/workflows/android-apk.yml` 用 Flutter 官方模板
现生成 `android/` 平台壳 → 补权限与签名 → `flutter build apk --release`，产物作为
Actions artifact 保留 30 天。推 `app/**` 会自动触发，也可以手动指定后端地址：

```bash
gh workflow run android-apk.yml \
  -f api_base_url=https://your-server.com \
  -f app_name=知聊
# 下载产物
gh run download --name datechat-apk-<sha>
```

**装到手机上要连哪个后端**：默认打进包里的地址是 `https://api.datechat.example`（占位）。
真机联调时用 `--dart-define=API_BASE_URL=http://192.168.x.x:8080` 重打一包，
或手动触发工作流时填 `api_base_url`。服务端必须已经部署并可被手机访问，
`127.0.0.1` 在手机上是手机自己，连不到你的电脑。

关于平台壳：仓库只维护 `app/lib/`，`android/` 不入库（由模板按 SDK 版本现生成，
避免在仓库里留一堆过期模板文件）。权限与签名由 `scripts/android/prepare_apk.py` 补齐，
其中最关键的两点：

- **INTERNET —— Flutter 的 release 主 manifest 默认不带，不补的话正式包上不了网**；
- release 默认用 debug key 签名（能装、上不了架），脚本会换成 CI 现场生成的
  RSA-2048 自签密钥，签出来是 v2/v3 签名。上架前请换成你自己的正式密钥。

iOS 包需要 macOS + Xcode + Apple 开发者账号（签名与描述文件），本仓库暂未提供。

## P0 已交付

- 后端可启动骨架：配置加载、zap 日志、GORM + MySQL、go-redis、优雅退出
- 中间件：TraceID、访问日志、Panic 恢复、CORS、按 IP 限流、JWT 鉴权
- 统一响应体与错误码体系（`errs` + `resp`），分页结构
- 自研迁移工具：`up / down / status`，带 `schema_migrations` 版本表
- 全量建表 SQL：**33 张业务表**（账号、关系、会话消息、通话计费、钱包订单、LBS、内容安全、运营后台）
- Flutter 骨架：Riverpod + go_router（StatefulShellRoute 四 Tab）+ Dio 拦截器 + 主题

## P1 已交付（账号体系 + 实名）

| 能力 | 实现要点 |
|---|---|
| 短信验证码 | 6 位随机码存 Redis（默认 5 分钟，一次性消费）；同号 60 秒冷却、单日 10 条上限；发送失败自动回滚冷却，不让用户白等 |
| 手机号登录/注册 | 验证码换双令牌；未注册自动建号，事务内写 `user` / `user_auth` / `user_profile` / `wallet` 四表，绝不留半条命数据 |
| 双 Token | access 2h + refresh 30d；refresh **单设备单会话 + 一次性轮换**，旧令牌重放直接判定失效并强制重登 |
| 实名认证 | 姓名 2~20 汉字、身份证 GB11643 校验位与出生日期双重校验；`user_realname` 上 `id_card_hmac` 唯一，一号一人；未通过可重提，通过后禁止变更 |
| 数据安全 | 手机号/姓名/身份证 AES-256-GCM 加密落库，另存 HMAC 供唯一索引；响应体只回脱敏值（138\*\*\*\*8000 / 110101\*\*\*\*\*\*\*\*1233） |
| 腾讯 IM | 登录时异步 `account_import`（失败不阻塞登录），下发 UserSig；UserSig 用 HMAC-SHA256 + zlib 自实现，不引 SDK |
| 通道可插拔 | 短信与实名都是 `Provider` 接口：`mock`（本地打日志/直接通过）与 `tencent`（腾讯云短信 / 人脸核身）由配置切换 |

### 接口清单

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| POST | `/v1/auth/sms/code` | 否 | 发送验证码。dev 下回带 `debug_code` 便于联调 |
| POST | `/v1/auth/login` | 否 | 手机号 + 验证码登录（自动注册），返回双令牌与 IM 材料 |
| POST | `/v1/auth/refresh` | 否 | 刷新令牌（一次性，旧 refresh 立即失效） |
| POST | `/v1/auth/logout` | 是 | 退出登录，吊销该设备刷新态 |
| GET | `/v1/auth/me` | 是 | 当前用户摘要（昵称/头像/脱敏手机号/实名状态） |
| GET | `/v1/im/usersig` | 是 | 重新签发 IM UserSig |
| POST | `/v1/realname/submit` | 是 | 提交实名（返回状态；腾讯云通道会返回 H5 活体地址） |
| GET | `/v1/realname/status` | 是 | 查询实名状态与脱敏信息 |
| POST | `/v1/realname/query` | 是 | 客户端完成活体后回查结果 |

### 账号相关错误码

| 码 | 含义 |
|---|---|
| 20002 | 账号被封禁 / 冻结 |
| 20004 | 验证码错误、过期或错误次数超限 |
| 20005 | 发送过于频繁（冷却中 / 当日超限） |
| 10102 | 登录态失效（refresh 被重放、已吊销） |
| 10103 | 无权限（未实名时访问受限接口） |

## P2 已交付（资料编辑 + 头像上传 + 他人主页）

| 能力 | 实现要点 |
|---|---|
| 资料编辑 | **局部更新**（指针语义"传了才改"，清空签名 ≠ 不改签名）；昵称/职业/签名/标签统一过敏感词：拦截级直接拒绝、替换级自动打码、转人工级落 `moderation_record`；生日按周岁换算并强制 ≥18 岁；城市必须命中 `region_city` |
| 头像上传 | 三段式：取凭证 → 直传 → 回调提交 URL。服务端只签名、不中转文件；提交时把 URL 反解成 `object_key`，再校验"这个文件是我们签发的、属于当前用户、未被审核拒绝"，堵死外链挂载 |
| 对象存储 | 腾讯云 COS 的 POST Object 直传签名自实现（`SignKey = HMAC-SHA1(SecretKey, KeyTime)`，`StringToSign = SHA1(policy)`，policy 里带 `content-length-range` 限制大小），不引 SDK；`provider=local` 时落本地盘并用 StaticFS 提供访问，**两条通道对客户端接口完全一致**，切换只改配置 |
| 他人主页 | 只返回公开字段（手机号、实名明文一律不下发）；在线状态走 Redis 心跳（TTL 2 分钟）不写库，主表活跃时间按 2 分钟最小间隔刷新；访问写 `user_visit`（按访客-被访者对去重），被对方拉黑时只标记 `blocked_by`，不写访问记录 |
| 内容安全 | `internal/pkg/textfilter`：词库从 DB 加载并每 5 分钟热更新（运营后台改词即时生效）；匹配前做去空白 / 全角转半角归一化，"加 微 信" 这类基础变体同样命中 |
| 新增表 | `upload_file`：上传台账（场景/通道/对象键/校验和/审核状态/是否被业务引用），承担"URL 合法性校验、审核留痕、孤儿文件回收"三件事 |

### 接口清单（P2 新增）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/v1/users/me/profile` | 我的资料，含生效定价（开启自定价且 >0 用自定价，否则全局价） |
| PUT | `/v1/users/me/profile` | 局部更新资料 |
| POST | `/v1/users/me/avatar` | 提交头像地址（必须来自上传接口） |
| POST | `/v1/users/me/heartbeat` | 在线心跳，返回保活秒数 |
| GET | `/v1/users/:uid` | 他人主页（含在线状态、关系标记） |
| POST | `/v1/uploads/sign` | 上传凭证：local 返回上传接口，cos 返回直传签名 |
| POST | `/v1/uploads/local` | 本地通道 multipart 上传（用 magic bytes 判断图片类型，不信客户端声明） |
| GET | `/v1/cities` | 城市字典 |

### 迁移

`migrations/0004_p2.up.sql`：新增 `upload_file`，并向 `app_config` 写入上传与资料相关运行时参数
（`upload.provider` / `upload.max_image_mb` / `upload.avatar_review` / `profile.*`）。

### 验收

```bash
bash scripts/verify-p2.sh        # 24 项断言，本机实测全绿
```

覆盖：资料读写与局部更新、敏感词拦截（昵称/签名）、城市与年龄校验、上传凭证、图片上传与非图片拒绝、
静态访问、头像设置与伪造外链拒绝、在线心跳、他人主页（关系/在线/定价）、访问记录、
数据库侧校验（`user_visit` / `upload_file.used` / `moderation_record`）。

### 客户端

新增 `features/profile/`：`profile_models` / `profile_api` / `profile_controller`、资料编辑页、
他人主页（`/user/:uid`）、头像更换（本地文件 multipart 或图片链接），「我的」页接入资料完整度。
`dart analyze lib` 与 `dart format` 均通过（27 个文件）。

> 头像选择器：工程未引入 `image_picker`（离线环境无法解析插件树），当前提供"本地文件路径"与
> "图片链接"两个可真实跑通上传链路的入口；接入插件后只需替换 `avatar_picker.dart` 里的取字节实现。

## P3 已交付（腾讯 IM + 会话/消息/已读/撤回）

| 能力 | 实现要点 |
|---|---|
| 腾讯 IM 服务端 | `account.IMClient` 统一封装管理员 UserSig 与 REST 公共参数；支持 `openim/sendmsg`、`admin_set_msg_read`、`admin_msgwithdraw`，业务错误从 HTTP 200 的 `ErrorCode` 解析 |
| 会话与消息 | `internal/modules/chat` 提供会话列表、消息历史、文本发送、已读、撤回；`client_msg_id` 与 `msg_id` 两级幂等，发送、会话摘要、双方成员游标和未读数在同一事务更新 |
| 回调镜像 | `/v1/im/callback` 直接挂在引擎（不走 JWT / 通用限流）；支持 `C2C.CallbackBeforeSendMsg`、`C2C.CallbackAfterSendMsg`、已读类回调及撤回类兼容命令；原始报文按 SHA-256 去重落 `im_callback_log` |
| 回调安全 | 强校验 `SdkAppid`；配置 Token 后校验 `Sign = sha256(Token + RequestTime)`，并限制时间偏差 1 分钟；生产环境禁止空 Token 和 `local_fallback` |
| 内容审核 | 消息发送接口与消息前回调都查双向拉黑并执行本地敏感词规则；拦截返回站内 `30002` 或腾讯回调协议 `120002`，命中结果落 `moderation_record(biz_type=1)` |
| 撤回 | 仅发送者可撤回，默认 2 分钟时间窗；真实通道使用回调记录的 `MsgKey` 调腾讯撤回，成功后本地消息状态与最后消息摘要同步更新 |
| Flutter | 新增 `features/chat/` 与真实会话列表：历史翻页、10 秒轮询、发送、进入即已读、本人消息长按撤回、未读角标；`/chat/:peerUid` 为独立全屏路由，他人主页可直接发起聊天 |
| 开发降级 | `im.local_fallback=true` 时无腾讯密钥也能走同一套接口写本地镜像，用于本机端到端验收；生产配置会拒绝启动，防止误把降级模式带上线 |

### 接口清单（P3 新增）

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| GET | `/v1/im/conversations` | JWT | 分页会话列表、最后消息、未读总数 |
| GET | `/v1/im/messages?peer_uid=&before_id=&limit=` | JWT | 与指定用户的消息镜像，按本地消息 ID 游标向前翻页 |
| POST | `/v1/im/messages` | JWT | 发送文本消息；请求带 `peer_uid / text / client_msg_id` |
| POST | `/v1/im/conversations/:peer_uid/read` | JWT | 清理腾讯 IM 与本地镜像未读计数 |
| POST | `/v1/im/messages/:msg_id/recall` | JWT | 撤回本人在时间窗内发送的消息 |
| POST | `/v1/im/callback` | 腾讯签名 | 消息前审核、消息后镜像、已读与撤回状态同步；始终按腾讯协议返回 HTTP 200 |

### 迁移与生产配置

`migrations/0005_p3.up.sql` 不新增业务表，扩展已有消息模型：

- `conversation`：最后消息类型与脱敏摘要；
- `conversation_member`：`last_msg_seq` 与会话列表过滤索引；
- `message`：`MsgKey`、客户端幂等键、撤回操作者、更新时间与查询索引，并将 `msg_id` 收紧为非空；
- `im_callback_log`：SDKAppID、命令、处理完成时间与排查索引。

生产接入前必须完成：

1. 设置真实 `sdk_app_id / secret_key`，开启 `import_enabled`，关闭 `local_fallback`；
2. 在腾讯云 IM 控制台设置公网 HTTPS 回调地址 `https://<域名>/v1/im/callback`，开启回调鉴权并把 Token 写入 `TENCENT_IM_CALLBACK_TOKEN`；
3. 启用“发单聊消息之前 / 之后”以及所需已读回调，并关闭“允许客户端禁用前后回调/审核”；
4. Android Flutter 已接原生 TIM SDK：有真实 UserSig 时用长连接收消息并显示系统通知，未配 IM 时自动回退到 10 秒轮询。App 进程彻底结束后的离线推送还需控制台厂商证书和 `timpush-configs.json`。

> 腾讯云官方文档说明：消息前回调超时只有 2 秒；自 2026-10-01 起，专业版该能力为独立增值项，开发版/旗舰版/企业版政策不同，上线前需按实际套餐确认。

### 验收

```bash
bash scripts/verify-p3.sh             # 20 项断言，本机实测全绿
bash scripts/flutter-offline-analyze.sh
```

覆盖：双账号登录、消息发送、`client_msg_id` 幂等、敏感词拦截、会话摘要、未读与历史、已读清零、反向消息、撤回与越权拒绝、错误 SdkAppid、消息前回调审核、消息后回调幂等、已读回调、数据库侧消息/回调/审核留痕；Go `build/vet/test` 与 Flutter `dart analyze/format` 全通过。

## P4 已交付（关系与内容安全）

| 能力 | 实现要点 |
|---|---|
| 拉黑 | `POST/DELETE /v1/users/:uid/block`、`GET /v1/users/me/blocks`；软删（`deleted_at`）而非物理删除，取消拉黑用 `ON DUPLICATE KEY UPDATE deleted_at=NULL`，双向判定统一过滤 `deleted_at IS NULL` |
| 发消息前拦截 | `chat` 模块发消息与 IM 消息前回调都先查双向拉黑，命中返回 `30001`；服务端拦截优于客户端置灰，改包绕不过 |
| 敏感词 | `internal/pkg/textfilter` 升级为 **Aho-Corasick 自动机**（原朴素子串扫描）：一次扫描报出全部命中，2000 词 / 1160 字约 16µs、2 次分配；词库 5 分钟热更新 |
| 云审核 | `internal/pkg/tms` 封装腾讯云 `TextModeration`：Content Base64、`Suggestion`（Block/Review/Pass）、Label/Score/Keywords；复用 TC3 签名 |
| 三层判定 | 本地 DFA → 云审核 → 统一裁决；云失败按 `cloud_fail_open` 决定是否放行，结果一律落 `moderation_record` |
| 举报 | `report` 表 + `uk_reporter_target` 去重 + 单日上限 + 理由白名单；`moderation_record` 复用，不再建第二张审计表 |
| 后台处置 | `admin_user` 复用（role 为 TINYINT）、JWT 增加 `TypeAdmin` 与 `Role`、独立 `AdminAuth` 中间件（用户令牌访问后台返回 `10103`）；封禁/解禁与举报处置都写 `admin_op_log` |

### 接口清单（P4 新增）

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| POST | `/v1/reports` | JWT | 提交举报（目标类型、目标 ID、理由、补充描述、证据） |
| GET | `/v1/users/me/blocks` | JWT | 我的黑名单 |
| POST | `/v1/users/:uid/block` | JWT | 拉黑 |
| DELETE | `/v1/users/:uid/block` | JWT | 取消拉黑 |
| POST | `/v1/admin/login` | — | 后台登录，下发带 role 的管理员令牌 |
| GET | `/v1/admin/reports` | 管理员 | 举报列表（按状态过滤） |
| POST | `/v1/admin/reports/:id/handle` | 管理员 | 处置举报（警告/封禁/忽略）并落审计 |
| GET | `/v1/admin/moderations` | 管理员 | 审核记录列表 |
| POST | `/v1/admin/moderations/:id/handle` | 管理员 | 人工复核结论 |
| POST | `/v1/admin/users/:uid/status` | 管理员 | 冻结/解封用户 |

### 验收

```bash
bash scripts/verify-p4.sh    # 17 项断言全绿
```

## P5 已交付（钱包与充值）

| 能力 | 实现要点 |
|---|---|
| 微信支付 v3 | `internal/pkg/wxpay`：SHA256withRSA 签名、APIv3 AES-256-GCM 资源解密、平台证书验签、下单/查单/回调解析 |
| 服务端定价 | 客户端只传 `package_id`，金额与金币一律取自 `recharge_package`，杜绝"改包 1 分钱买一万金币" |
| 三道幂等闸门 | ① `pay_notify_log.uk_trade_event` 挡重复投递；② 订单 `status` 条件更新；③ `wallet_transaction.uk_biz` 保证同一订单只写一条流水（真并发下整笔回滚） |
| 账本 | `wallet_transaction` 是唯一账本，只增不改删；`wallet` 表余额用行锁 + 乐观锁双保险 |
| 兜底 | 主动查单补单（回调丢失）、超时未付关单、对账（余额合计 vs 流水净额） |

### 接口清单（P5 新增）

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| GET | `/v1/wallet` | JWT | 余额、累计充值/消费 |
| GET | `/v1/wallet/transactions` | JWT | 金币流水 |
| GET | `/v1/wallet/packages` | JWT | 充值档位 |
| POST | `/v1/wallet/recharge` | JWT | 下单，返回支付参数（未配商户号时为 mock） |
| POST | `/v1/wallet/recharge/:order_no/sync` | JWT | 主动查单补单 |
| POST | `/v1/pay/wechat/notify` | 微信签名 | 支付回调；无验签能力时直接 403，绝不放无签名请求发货 |
| POST | `/v1/pay/mock/notify` | 无（仅 dev） | 模拟回调，用于端到端验收 |

### 验收

```bash
bash scripts/verify-p5.sh    # 13 项断言全绿
```

覆盖：钱包初始化、档位、下单、模拟支付到账、**重复回调不重复加币**（余额不变、流水 1 条、回调日志 1 条）、金额不符被拒、对账一致。

## P6 已交付（通话与计费）

| 能力 | 实现要点 |
|---|---|
| 进房凭据 | `internal/pkg/trtc` 用与 IM 相同的 TLS 算法签 UserSig；房间号是随机 uint32（不是自增，避免被猜房间号蹭进别人通话）；未配密钥时按 `trtc.mock_enabled` 下发模拟房间（生产硬拦） |
| 状态机 | `init → ringing → talking → ended/canceled/rejected/timeout`；每次跃迁都走 `WHERE status IN (...)` 条件更新（并发由数据库裁决）并写 `call_event_log` |
| 计费 | **接通即扣第 1 分钟**，心跳按服务端时钟续扣后续分钟；`call_segment.uk_session_seq` 是天然幂等位，`wallet_transaction.biz_id = {通话号}#{分钟序号}` 保证重复续扣不会二次扣款 |
| 余额耗尽 | 续扣失败即按 `end_reason=2` 自动挂断，并把结束时间回拨到"已付费的最后一分钟末尾"，保证时长与账单自洽，不会出现负数余额 |
| 掉线兜底 | 后台巡检协程每 15 秒扫一次：振铃超时未接、心跳超时、通话中余额耗尽；客户端不发心跳服务端也能收尾 |
| 结算 | 挂断时按 `platform_rate` 拆分：被叫收益入账（`biz_type=6`）、平台抽成留档；`total_coins = billable_minutes × price_per_min` 恒等 |
| 对账接口 | 通话详情返回 `consistency`：`amount_matches` / `minutes_match_sec` / `segment_matches` 三个自检结论，验收不用人工对数 |

### 接口清单（P6 新增）

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| GET | `/v1/call/price?user_id=&media_type=` | JWT | 通话定价（被叫自定价优先，其次全局价） |
| POST | `/v1/calls` | JWT | 发起通话，返回会话 + 进房凭据 |
| GET | `/v1/calls` | JWT | 通话记录 |
| GET | `/v1/calls/:call_no` | JWT | 详情：会话 + 分钟切片 + 事件流水 + 账单一致性 |
| POST | `/v1/calls/:call_no/accept` | JWT（被叫） | 接听，进入计费 |
| POST | `/v1/calls/:call_no/reject` | JWT（被叫） | 拒接 |
| POST | `/v1/calls/:call_no/cancel` | JWT（主叫） | 取消（振铃中） |
| POST | `/v1/calls/:call_no/hangup` | JWT | 挂断并结算 |
| POST | `/v1/calls/:call_no/heartbeat` | JWT | 心跳：续扣 + 回传余额耗尽/超时结论 |
| POST | `/v1/admin/calls/sweep` | 管理员 | 手动触发巡检（正常由后台协程驱动） |

### 计费不变量

```
billable_minutes == ceil(duration_sec / 60)
total_coins      == billable_minutes × price_per_min
payee_income + platform_fee == total_coins
sum(call_segment.coins)    == total_coins
```

### 验收

```bash
bash scripts/verify-p6.sh    # 完整跑完约 90 秒（需要真实跨过一分钟）
```

覆盖：定价查询、并发呼叫被拒、接通即扣、心跳幂等、挂断结算与三项一致性、被叫收益、**余额耗尽自动挂断**、心跳超时由服务端兜底、账本对账、拉黑后禁止呼叫。

> Android 客户端已集成 `tencent_rtc_sdk`，可用服务端下发的 SDKAppID / UserSig 进入真实语音、视频房间。要两台手机真正互听互见，必须配置 `TENCENT_TRTC_SECRET_KEY`（或复用同应用的 IM 密钥）并关闭 `trtc.mock_enabled`；模拟房间只验收呼叫与计费链路。

## P7 已交付（LBS 与在线）

| 能力 | 实现要点 |
|---|---|
| 附近的人 | 检索走 **Redis GEO**（`GEOSEARCH`），MySQL 只存最后上报坐标用于兜底与审计；用 SQL 算距离会把数据库压垮 |
| 距离隐私 | 排序用真实距离，**对外只下发区间文本**（"3km 以内"）；`nearby.distance_fuzzy=false` 时才下发精确值，仅供内网联调 |
| 在线状态 | 只写 Redis（`presence:user:{uid}`，TTL 120s），绝不写主库；写方是心跳接口，读方是附近/访客列表，两边共用 `internal/pkg/presence` 的 key，避免各写一套前缀导致"所有人永远离线" |
| 筛选 | 性别与年龄在内存里过滤：Redis GEO 不支持 WHERE，交给 SQL 又会把距离计算带回数据库 |
| 排序 | 先按距离升序，再按筛选条件裁剪，最后内存分页（GEO 不支持分页） |
| 防刷与防脏数据 | 坐标非法（越界或 0,0）直接拒绝——脏坐标进 GEO 会让所有人的列表一起飘；位置上报按 `min_report_interval_sec` 节流，MySQL 侧不跟着高频写 |
| 谁看过我 | 复用 P2 的 `user_visit`，新增 `read_at` 表达未读；进列表即标记已读 |
| 拉黑 | 附近列表双向过滤拉黑关系，与 P4 的"发消息前拦截"共用同一份关系 |

### 接口清单（P7 新增）

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| POST | `/v1/users/me/location` | JWT | 上报坐标，返回是否被节流 |
| GET | `/v1/nearby?lng=&lat=&gender=&age_min=&age_max=&radius=` | JWT | 附近的人（经纬度必填） |
| GET | `/v1/users/online?ids=1,2,3` | JWT | 批量查在线，最多 200 个 |
| GET | `/v1/users/me/visits` | JWT | 谁看过我 + 未读数 |
| POST | `/v1/users/me/visits/read` | JWT | 标记访客已读 |

### 验收

```bash
bash scripts/verify-p7.sh    # 约 6 秒
```

覆盖：坐标校验（0,0 与越界被拒）、上报节流、附近的人**按距离升序**、超半径被过滤、自己不出现、距离按区间模糊、在线/离线在列表里可区分（离线即前端置灰依据）、性别筛选、年龄筛选、拉黑后消失与恢复、谁看过我与未读清零、缺经纬度被拒、GEO 与 MySQL 双侧留痕。

> 真机连续定位需要 `geolocator` 插件与系统定位权限。当前客户端用 `LocationSource` 做了桥接：真机未接入时返回 null 并明确提示，联调期可用"设置坐标"入口手动指定，链路（上报 → 检索 → 排序 → 在线）照样能完整跑通。

## P8 已交付（运营后台与上线准备）

### 运营后台（`internal/modules/ops`）

| 能力 | 实现要点 |
|---|---|
| 概览 | 一条 SQL 用子查询汇总今日新增/封禁/充值金额/通话分钟/待处理举报，不做十次 COUNT |
| 用户检索 | 支持 UID、昵称、**手机号**（走 HMAC 精确命中，手机号是密文存不了 LIKE）；后台也只展示脱敏号 |
| 用户详情 | 账号 + 资料 + 钱包 + 拉黑/被拉黑/通话/收益统计 + 最近举报与订单 |
| 封禁 | `POST /admin/users/:uid/ban` 必填原因、可带天数（写 `ban_until`）；**空原因直接拒绝** |
| 审计 | 每一次状态变更都写 `admin_op_log`（before/after 快照）；日志写失败会明确报错而不是"改了但没留痕" |
| 越权 | 后台路由全挂 `AdminAuth`，用户令牌访问一律拒绝 |

### 可观测

| 能力 | 实现要点 |
|---|---|
| 指标 | `internal/pkg/metrics` 分片 map + 原子计数，输出标准 Prometheus 文本；不引第三方客户端，够用且不增加构建依赖 |
| 标签 | 用**路由模板**（`/v1/users/:uid`）而不是真实路径，否则带 UID 的路径会把时序基数打爆 |
| 慢请求 | 超过 `observe.slow_request` 的请求单独打 warn——混在 info 访问日志里没人看得见，而它往往是缺索引的第一个信号 |
| 端点 | `GET /metrics`（可选 `?token=` / `X-Metrics-Token`；生产必须配令牌且只在内网开放） |

### 压测与留存

```bash
go run ./cmd/bench   -c configs/config.dev.yaml -users 50 -dur 30s -rps 40   # 限速率，看真实延迟
go run ./cmd/bench   -c configs/config.dev.yaml -users 50 -dur 30s            # 不限速，压到限流器上限
go run ./cmd/cleanup -c configs/config.dev.yaml                                # 默认 dry-run
go run ./cmd/cleanup -c configs/config.dev.yaml -apply                         # 真删（分批 2000 行）
```

- 压测自己注册账号、拿令牌、按真实比例混合请求；**429 单独统计为"被限流"，不计入失败率**——否则打满限流器就会误判不通过。
- 留存期按表区分：短信 30 天、登录 90 天、回调/通话事件/支付 180 天、后台操作审计 730 天；**聊天消息与订单不在自动清理范围内**（那是用户资产）。

### 合规

| 项 | 实现 |
|---|---|
| 实名门槛 | `compliance.realname_required_for_call`：未实名不能发起付费通话 |
| 未成年人 | `compliance.min_age`（默认 18），由 `/v1/app/agreements` 下发给客户端 |
| 充值额度 | 未实名用户单日充值上限（含未支付订单，防并发绕过） |
| 协议 | `GET /v1/app/agreements` 返回隐私政策/用户协议链接与版本号，未登录也能取 |
| 日志脱敏 | 后台手机号只给脱敏值；已注销账号超留存期后清空手机号密文 |

### 发布

- `server/Dockerfile`：多阶段构建，运行镜像只有静态二进制 + ca-certificates，非 root 运行；
- `docker-compose.yml` 增加 `api` 服务，带 healthcheck 与依赖就绪等待；
- 启动顺序为先迁移再起服务（容器内串行执行）。

### 接口清单（P8 新增）

| 方法 | 路径 | 鉴权 | 说明 |
|---|---|---|---|
| GET | `/v1/admin/overview` | 管理员 | 运营概览 |
| GET | `/v1/admin/users` | 管理员 | 用户列表（keyword / status 分页） |
| GET | `/v1/admin/users/:uid` | 管理员 | 用户详情 |
| POST | `/v1/admin/users/:uid/ban` | 管理员 | 封禁（days + reason，reason 必填） |
| POST | `/v1/admin/users/:uid/unban` | 管理员 | 解禁 |
| GET | `/v1/admin/orders` | 管理员 | 充值订单 |
| GET | `/v1/admin/calls` | 管理员 | 通话记录 |
| GET | `/v1/admin/oplogs` | 管理员 | 后台操作日志 |
| GET | `/metrics` | 可选令牌 | Prometheus 指标 |
| GET | `/v1/app/agreements` | 无 | 协议与最低年龄 |

### 验收

```bash
bash scripts/verify-p8.sh    # 约 15 秒
```

覆盖：后台登录与概览、按 UID/昵称/手机号检索、手机号脱敏、用户详情、封禁（含天数与原因）与解禁、审计日志留痕、空原因被拒、订单/通话/日志查询、用户令牌越权被拒、/metrics 三类断言、协议接口、未实名禁呼与实名后放行、留存清理 dry-run 不删数据、压测通过（P95 2.8ms、失败率 0）。

## 本地启动
### 方式 A：有 Docker（推荐，MySQL 8.0）

```bash
cd server
docker compose up -d          # MySQL 8 + Redis 7 + Adminer(:8081)
go mod tidy                   # 首次拉依赖
make migrate                  # 执行 migrations/*.up.sql
make run
```

### 方式 B：无 Docker（本机直接跑进程）

适用于没有管理员权限、装不了 Docker Desktop 的机器（本仓库就是在这种环境下验证的）。

```bash
# 1. 启动 MySQL + Redis（数据目录在 .local/，首次会自动初始化 MySQL）
bash scripts/dev-services.sh start
bash scripts/dev-services.sh status

# 2. 建库建用户（仅首次，root 无密码）
/opt/anaconda3/bin/mysql --socket=.local/mysql/mysql.sock -uroot -e "
CREATE DATABASE IF NOT EXISTS datechat DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'datechat'@'127.0.0.1' IDENTIFIED BY 'datechat_pass';
GRANT ALL PRIVILEGES ON datechat.* TO 'datechat'@'127.0.0.1'; FLUSH PRIVILEGES;"

# 3. 迁移（MySQL 5.7 需要走兼容脚本，见下方说明）
cd server && make migrate-local57

# 4. 启动
make run
```

> **为什么有 `migrate-local57`**：正式 schema 面向 MySQL 8.0，用了 `utf8mb4_0900_ai_ci` 排序规则，5.7 不识别。
> 该目标会把迁移脚本复制到 `.local/migrations57/` 并替换 collation 后执行，仓库里的 `migrations/*.sql` 保持 8.0 原样。
> **生产 / CI 一律用 MySQL 8.0 + `make migrate`。**

### 3. 起服务

```bash
cd server
make run
curl -s localhost:8080/healthz
curl -s localhost:8080/readyz
curl -s localhost:8080/v1/ping
```

### 4. 一键验收

```bash
bash scripts/verify-p0.sh          # 11 项  健康检查 / 统一响应 / 404 / 405 / TraceID / 限流
bash scripts/verify-p1.sh          # 15 项  发码→登录→刷新→重放拦截→实名→退出
bash scripts/verify-p2.sh          # 24 项  资料编辑→上传→头像→主页→在线状态
bash scripts/verify-p3.sh          # 20 项  消息→会话→未读→已读→撤回→回调幂等与审核
bash scripts/verify-p4.sh          # 17 项  拉黑→敏感词拦截留痕→举报→后台处置与审计
bash scripts/verify-p5.sh          # 13 项  充值下单→到账→回调幂等→金额校验→对账
bash scripts/verify-p6.sh          # 34 项  通话→心跳→计费→余额耗尽挂断→账单一致（约 90s）
bash scripts/verify-p7.sh          # 28 项  上报坐标→附近排序→模糊距离→在线置灰→访客
bash scripts/verify-p8.sh          # 30 项  后台运营台→指标→合规→留存清理→压测
```

任意一个脚本失败都会以非 0 退出，可直接挂到 CI 上。脚本之间共享同一个库，
建议按 P0 → P8 顺序跑（后阶段的脚本会清理/复用前面造的数据）。

### 全链路回归结果（2026-10-31，dev 环境真机跑出）

| 阶段 | 断言 | 结果 |
|---|---|---|
| P0 基础设施 | 11 | ✅ 全绿 |
| P1 登录鉴权 | 15 | ✅ 全绿 |
| P2 资料与文件 | 24 | ✅ 全绿 |
| P3 IM 与审核 | 20 | ✅ 全绿 |
| P4 关系与内容安全 | 17 | ✅ 全绿 |
| P5 钱包与充值 | 13 | ✅ 全绿 |
| P6 通话与计费 | 34 | ✅ 全绿 |
| P7 LBS 与在线 | 28 | ✅ 全绿 |
| P8 上线准备 | 30 | ✅ 全绿 |
| **合计** | **192** | **0 失败** |

服务端 `go build` / `go vet` / `go test ./internal/...` 全通过；客户端
`dart analyze lib` → `No issues found!`（49 个文件）。

### 5. 起客户端

```bash
cd app
flutter pub get
flutter run                       # 默认连 http://127.0.0.1:8080
```

## 环境变量

| 变量 | 说明 | 默认 |
|---|---|---|
| `APP_ENV` | 选择 `configs/config.{env}.yaml` | `dev` |
| `MYSQL_PASSWORD` | 覆盖配置文件中的库密码 | - |
| `JWT_SECRET` | 覆盖 JWT 密钥 | - |
| `TENCENT_IM_SECRET_KEY` | 腾讯云 IM UserSig 与服务端 REST 密钥 | 空（dev 使用 local fallback） |
| `TENCENT_IM_CALLBACK_TOKEN` | 腾讯云 IM 回调应用层签名 Token | 空（生产必填） |
| `TENCENT_TRTC_SECRET_KEY` | 腾讯云 TRTC 进房 UserSig 密钥 | 空（dev 使用 mock room） |

配置文件中支持 `${VAR}` 语法，加载时做环境变量展开。

## 密钥配置

真实密钥一律不进仓库。三种注入方式，**优先级：进程环境变量 > `.env` 文件**（容器部署只用前者即可）：

```bash
bash scripts/secrets.sh init                       # 从 .env.example 生成 .env（权限 600，已被 .gitignore 忽略）
bash scripts/secrets.sh gen 48                     # 生成一个随机密钥（只打印，不写入）
bash scripts/secrets.sh set JWT_SECRET=xxx ...     # 写入/更新 .env，保留注释，全部成功才落盘
bash scripts/secrets.sh check --env prod           # 上线前自检：缺失/过短/格式错误逐条报出
cd server && APP_ENV=prod go run ./cmd/api -check-config   # 真实加载一遍配置（不连库、不监听端口）
```

`-check-config` 只做「加载 + 校验 + 脱敏打印」，密钥原文不会出现在输出里（只显示字节数），可安全用于 CI。

### 需要的密钥

| 分组 | 变量 | 获取位置 |
|---|---|---|
| 应用 | `JWT_SECRET` `SECURITY_DATA_KEY` `SECURITY_HMAC_KEY` | 自生成：`bash scripts/secrets.sh gen 48` |
| IM | `TENCENT_IM_SDK_APP_ID` `TENCENT_IM_SECRET_KEY` `TENCENT_IM_CALLBACK_TOKEN` | 控制台 → 即时通信 IM → 应用（密钥 / 回调配置） |
| 短信 | `TENCENT_SMS_SECRET_ID` `TENCENT_SMS_SECRET_KEY` `TENCENT_SMS_SDK_APP_ID` `TENCENT_SMS_SIGN_NAME` `TENCENT_SMS_TEMPLATE_ID` | 控制台 → 短信 → 应用 / 签名模板 |
| 实名 | `TENCENT_FACEID_SECRET_ID` `TENCENT_FACEID_SECRET_KEY` `TENCENT_FACEID_RULE_ID` `REALNAME_REDIRECT_URL` | 控制台 → 人脸核身 → 业务流程 |
| 对象存储 | `TENCENT_COS_SECRET_ID` `TENCENT_COS_SECRET_KEY` `TENCENT_COS_REGION` `TENCENT_COS_BUCKET` `TENCENT_COS_CDN_DOMAIN` | 控制台 → 对象存储 → 存储桶 |

`SECURITY_DATA_KEY` / `SECURITY_HMAC_KEY` 一旦投入生产就**不能随意改动**：手机号与身份证已按它们加密落库，换密钥需要保留旧密钥做双读迁移。

### 启动期硬拦截

以下情况服务**拒绝启动**，而不是带着错误配置安静运行：

- 生产环境 `jwt.secret` / `security.data_key` / `security.hmac_key` 仍是 `dev*` 前缀的示例值；
- 生产环境 `im.secret_key` 为空，或 `im.sdk_app_id` 非正整数，或 `im.import_enabled=false`；
- 生产环境开启 `im.local_fallback`（会把消息写本地镜像而非腾讯云）；
- 生产环境 `im.callback_token` 为空（回调将无法验签）。

上线前还需在腾讯云控制台完成：IM 的「发消息前 / 发消息后 / 已读 / 撤回」回调 URL 指向 `https://{域名}/v1/im/callback`，并填写与 `TENCENT_IM_CALLBACK_TOKEN` 一致的 Token。

## 真实密钥获取指引（腾讯云）

### 先看清门槛

有几项**个人账号拿不到**，先确认主体资质再决定投入（2026-10 现行规则）：

| 服务 | 主体要求 | 额外审核 | 个人账号可行吗 |
|---|---|---|---|
| 即时通信 IM | 无特殊要求 | 无 | ✅ 可以 |
| 对象存储 COS | 无特殊要求 | 无 | ✅ 可以 |
| 国内短信 | **需企业资质** | 签名/模板审核（通常 1 个工作日内） | ❌ 自 2025-09-18 起个人无法申请国内短信自用签名 |
| 人脸核身（慧眼） | **需企业实名认证 + 行业资质** | 3–5 个工作日 | ❌ 个人不可用 |

社交/直播行业在人脸核身开放行业清单内，但要求企业主体与对应资质文件。若暂时拿不到，可先把 `sms.provider=mock`、`realname.provider=mock` 保持不动（dev 默认即是），这两项不影响 P0–P3 的联调与验收。

### 1. 即时通信 IM（P3 用）

1. 打开 [IM 控制台](https://console.cloud.tencent.com/im) → **应用管理** → **创建新应用**（选离用户近的数据中心）。
2. 在应用的 **SDKAppID 列** 拿到 `TENCENT_IM_SDK_APP_ID`。
3. 操作列点 **查看密钥** → **显示密钥**（需身份验证），复制填入 `TENCENT_IM_SECRET_KEY`。

> 注意：这是 IM 的**应用密钥**，与 CAM 的 SecretId/SecretKey 不是一回事，别混填。

4. **回调配置**（关键，P3 的审核与落库依赖它）：应用 → **回调配置** → 回调 URL 填 `https://{你的域名}/v1/im/callback`，Token 自填一个并同步写入 `TENCENT_IM_CALLBACK_TOKEN`；勾选「发单聊消息之前」「发单聊消息之后」「单聊消息已读」「单聊消息撤回」。
5. 套餐核对：消息前回调在部分套餐下属独立增值项，且腾讯云给回调的超时只有约 2 秒——审核逻辑必须够快，否则会拖慢发消息。

### 2. CAM 密钥（短信 / 人脸核身 / COS 共用）

[访问管理控制台](https://console.cloud.tencent.com/cam/capi) → **API 密钥管理** → 新建密钥，得到 `SecretId` / `SecretKey`。

**不要直接用主账号密钥**：新建子账号，按服务授予最小权限（如只给 COS 的 `PutObject`），泄露时影响面可控。

### 3. 国内短信

1. [短信控制台](https://console.cloud.tencent.com/sms) → 先做**资质报备**（企业营业执照 + 法人 + 业务管理员材料，加盖公章）。
2. **创建签名**：签名内容需与营业执照全称/简称一致（APP、网站、公众号、小程序类签名已被运营商拦截，不要用）。
3. **创建模板**：验证码模板形如 `您的验证码是{1}，5分钟内有效`，变量取 6 位纯数字。
4. 审核通过后，填入 `TENCENT_SMS_SIGN_NAME`（签名内容，不含方括号）、`TENCENT_SMS_TEMPLATE_ID`、`TENCENT_SMS_SDK_APP_ID`。

个人主体无法走通时：可将账号升级为企业认证，或用企业授权书申请「他用资质」。

### 4. 人脸核身（实名）

1. [人脸核身控制台](https://console.cloud.tencent.com/faceid) 开通服务（需企业实名）。
2. **自助接入 → 创建业务流程**：选应用场景、上传行业资质、选「增强版人脸核身」、配置比对库（权威库/自传照片）与活体检测方式。
3. 审核通过（3–5 个工作日）后系统分配 `TENCENT_FACEID_RULE_ID`。
4. `REALNAME_REDIRECT_URL` 填核身完成后的回跳地址，必须与控制台配置一致，否则用户核身完跳不回来。

### 5. 对象存储 COS（头像上传）

1. [COS 控制台](https://console.cloud.tencent.com/cos) → **存储桶列表 → 创建存储桶**，得到形如 `datechat-1250000000` 的桶名和地域（`ap-guangzhou` 等）。
2. **跨域访问 CORS 设置**（最容易漏，漏了前端直传会静默失败——浏览器只报 OPTIONS 预检被拒）：
   - 来源 Origin：填你的域名，如 `https://app.example.com`；调试期可临时加 `http://localhost:*`，**生产不要填 `*`**（带凭证时浏览器会拒绝通配）。
   - Methods：勾 `GET` `POST` `PUT` `HEAD`。
   - AllowedHeaders：显式写 `content-type`、`x-cos-*`、`authorization`；部分浏览器对 `*` 支持不一致。
   - ExposeHeaders：需要读 `ETag` 时显式声明。
3. 填入 `TENCENT_COS_SECRET_ID` / `TENCENT_COS_SECRET_KEY` / `TENCENT_COS_REGION` / `TENCENT_COS_BUCKET`；启用 CDN 时再填 `TENCENT_COS_CDN_DOMAIN`。

### 拿到之后

```bash
bash scripts/secrets.sh set TENCENT_IM_SDK_APP_ID=... TENCENT_IM_SECRET_KEY=...   # 逐项写入
bash scripts/secrets.sh check --env prod
cd server && APP_ENV=prod go run ./cmd/api -check-config
```

同时把 `configs/config.dev.yaml` 的 `im.local_fallback` 设为 `false`、`im.import_enabled` 设为 `true`，即可做真实联调。

## P0 验收清单

- [ ] `docker compose up -d` 后 `mysql` / `redis` 均 healthy
- [ ] `make migrate` 可重复执行，第二次输出 "no pending migration"
- [ ] `curl localhost:8080/healthz` → `{"code":0,"msg":"ok",...}`
- [ ] `curl localhost:8080/readyz` → 数据库与 Redis 均 `ok`
- [ ] `curl localhost:8080/v1/ping` → 返回版本与服务器时间
- [ ] 连续请求 `/v1/ping` 超过 50 rps → 返回 `10003 请求过于频繁`
- [ ] `curl -X POST localhost:8080/v1/ping` → `405`，且响应体是统一结构
- [ ] `flutter run` 起 App，四个 Tab 可切换且各自状态保持

## 本机安装与实测状态（2026-10-31）

| 组件 | 状态 | 说明 |
|---|---|---|
| Go 1.27.3 | ✅ 已安装 | 装在 conda 缓存，`~/.local/bin/go` 为包装脚本，`go version` 可直接用 |
| MySQL 5.7.24 | ✅ 运行中 | 复用 Anaconda 自带 mysqld，数据目录 `.local/mysql/data`，端口 3306 |
| Redis 7.2.5 | ✅ 运行中 | 源码编译，端口 6379，`redis-cli ping` → PONG |
| 34 张表 + 种子数据 | ✅ 已建 | 含 P1 `sms_send_log`、P2 `upload_file`，P3 扩展会话/消息/回调字段与索引 |
| Go 编译 / 测试 / 运行 | ✅ 全通过 | `build` / `vet` / `test ./internal/...`，P0–P8 九个验收脚本 192 项断言全绿 |
| Flutter SDK（最小分析版） | ✅ 已安装 | `.local/flutter`，Dart 3.13.7；`dart analyze lib`：49 文件 No issues found |
| Docker | ❌ 无法安装 | Docker Desktop 需要管理员权限 + GUI 守护进程，本机无 sudo |

### 已实测通过

- `migrations/0001_init.up.sql` 在真实 MySQL 上建出 **32 张表**，无语法错误
- `migrations/0003_p1.up.sql` 建出 `sms_send_log`，总表数 **33 张**
- `migrations/0004_p2.up.sql` 建出 `upload_file`，总表数 **34 张**；`0005_p3.up.sql` 在 MySQL 5.7 真实扩展会话/消息/回调字段与索引
- `scripts/verify-p3.sh`：消息/会话/未读/已读/撤回、回调幂等和消息前审核 **20 项断言全绿**
- `migrations/0002_seed.up.sql` 写入 10 项配置、4 个充值档位、6 个城市、6 个敏感词、1 条通话定价
- **P1 单元测试全绿**：`crypto`（AES-GCM 往返/篡改检测/密钥隔离/HMAC 稳定性）、`phone`（8 种写法归一化 + 6 类非法号段）、`idcard`（GB11643 校验位/生日/年龄/性别）、`tlsig`（UserSig 结构/确定性/签名复算/过期）
- **P1 原生 SQL 在真实 MySQL 上逐条跑通**：设备 upsert 只留一行、实名状态与 `user_profile` 冗余同步、同身份证第二次插入被 `uk_id_card` 以 1062 拦截
- **两个 Lua 脚本在真实 Redis 上验证**：验证码"命中即删 + 错误累加 + 超限锁定"、refresh"一次性轮换 + 重放返回 0"


### 编译与运行状态：已打通

之前卡住的 `go mod tidy` 已解决（沙箱早期的 rename/delete 限制后来放开），现在 `server/go.sum` 已生成并随仓库提交：

```bash
cd server
go build ./...     # ✅ 通过，无告警
go vet ./...       # ✅ 通过
go test ./...      # ✅ 全部 ok（config / middleware / crypto / errs / idcard / jwt / phone / tlsig）
make run           # ✅ 服务正常启动，/healthz 与 /readyz 均 ok
bash ../scripts/verify-p1.sh   # ✅ 16 项断言全过
```

`verify-p1.sh` 的实际输出（真机跑出来的，不是示意图）：

```
0. 健康检查                 ✓
1. 发送验证码               ✓  debug_code=412349
2. 发送过于频繁应被拒绝      ✓  20005
3. 错误验证码应失败          ✓  20004
4. 正确验证码登录            ✓  uid=6 is_new_user=True，手机号回显 138****3828
5. 访问需要登录的接口        ✓  /v1/auth/me 昵称=聊友3828
6. 无 token 访问应 401      ✓  10100
7. 刷新令牌（一次性）        ✓  已换发新令牌
8. 重放旧 refresh 应被拒绝   ✓  10102
9. 提交实名（mock 即时通过） ✓  张* / 110101********3423
10. 查询实名状态            ✓  已认证
11. 同身份证换账号提交        ✓  该身份证已被其他账号绑定
12. IM UserSig 签发         ✓  u_6，长度 192
13. 退出登录                ✓
14. 退出后刷新应失效         ✓  10102
结果：通过 16 项，失败 0 项
```

UserSig 另做了独立解码验证：`base64 → zlib → JSON` 得到 `TLS.ver=2.0 / identifier=u_8 / sdkappid=1400000000 / expire=604800`，且签名可用密钥复算一致。

数据库侧抽查：注册 7 个用户，`phone_cipher` 均为密文（同一手机号两次加密结果不同）；实名记录 `channel=0 provider_trace=mock-*` 且 `user_profile.realname_status` 同步为 2；`user_login_log` 7 成功 3 失败（对应脚本里的错误验证码用例）；`sms_send_log` 7 条。

### 客户端已通过静态分析验证（本机实测，2026-10-31）

原以为这台机器装不了 Flutter SDK。实测找到一条小路：**从 2.16GB 的官方 zip 里按 HTTP Range 只抽 250MB**，
跳过其中 1.35GB 的平台 artifacts（只有编译/运行才需要），拼出一个可运行的 SDK：

| 组成 | 压缩体积 | 作用 |
|---|---|---|
| `bin/cache/dart-sdk` | 216.8 MB | Dart 3.13.7，analyze 的引擎 |
| `bin/cache/flutter_tools.snapshot` | 18.1 MB | flutter 命令行本体 |
| `packages/` | 12.6 MB | 框架源码 + flutter_test |
| `bin/internal` 等脚本 | <1 MB | 启动脚本与版本文件 |
| ~~`bin/cache/artifacts`~~ | ~~1348 MB~~ | 跳过：iOS/Android/Web 编译产物 |

跑通的证据：

```
$ .local/flutter/bin/flutter --version
Flutter • channel main • unknown source
Framework • revision f61308268b • 2026-10-31
Engine • hash 9e025265bef30b838c0c1b68cad19decf97048f2 (revision 89c39edfdf)
Tools • Dart 3.13.7 • DevTools 2.60.0

$ dart analyze lib          # 已带 flutter_lints 5.0.0 规则
Analyzing lib...
No issues found!            # 20 个文件 / 1962 行

$ dart format --output=none --set-exit-if-changed lib
Formatted 20 files (0 changed)
```

分析过程中只暴露过 1 个真实问题，已修：`api_exception.dart` 里 `DioExceptionType` 的 switch 未穷尽
（dio 5.11 新增了 `transformTimeout`），改成 `default` 兜底，避免以后升级 dio 又断。

**注意**：这是 `dart analyze`（analyzer 内核与 `flutter analyze` 相同、依赖与 SDK 版本都对得上），
但它不覆盖 `flutter pub get` 的完整解析，也不等于编译产物正确。真机跑之前仍建议在正常网络下
`flutter pub get && flutter analyze && flutter run`。

复现脚本：`bash scripts/flutter-offline-analyze.sh`（检测 SDK → 重建 package_config → 分析 + 格式检查）。

### 三条替代路线（装在正常机器上时用第 1 条就好）

| 方式 | 覆盖度 | 成本 | 适合场景 |
|---|---|---|---|
| **1. 云端 CI**（仓库已带 `.github/workflows/flutter-analyze.yml`） | 100%，等价于本地 `flutter analyze` + `flutter build web` | 零本地安装，push 后 5 分钟出结果 | 网络差、不想装 SDK、团队协作 |
| **2. 最小化抽取 SDK**（本次采用，脚本已入库） | ~95%，analyze/format 全通，不含编译 | 下载 250MB，约 20–30 分钟 | 想在本机跑分析但拉不动 2.16GB |
| **3. 只装 Dart SDK（215MB）** | ~50%，只能 `dart format` 查语法，查不到 API 类型 | 下载 215MB | 只想确认没有语法错误 |

本次没用第 3 条，因为语法错误可以用眼睛看，**真正会踩的是 API 签名变更**（`CardThemeData`、
`withValues`、`DioExceptionType` 这类），只有真 analyzer 才抓得到。

### 仍未做的：真机运行

`flutter run` 需要各平台 artifacts（`bin/cache/artifacts`，1.35GB），本机带宽下不现实。
装到正常机器后执行：

```bash
cd app
flutter pub get
flutter analyze      # 应输出 No issues found
flutter run          # 或 flutter run -d chrome
```

如果报错，把输出发我，我按行修。

## 后续阶段

P1 账号体系 ✅ → P2 用户主页 ✅ → P3 IM 接入 ✅ → P4 关系与内容安全 ✅ → P5 钱包充值 ✅ → P6 通话计费 ✅ → P7 LBS 与在线状态 ✅ → P8 运营后台与上线 ✅。
