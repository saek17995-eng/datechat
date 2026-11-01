# DateChat · Flutter 客户端

## 目录约定

```
lib/
├── bootstrap.dart          启动装配：初始化存储 → 注入 Provider → runApp
├── main.dart               仅调用 bootstrap
├── core/
│   ├── config/             环境配置（dev/prod，支持 --dart-define）
│   ├── di/                 Riverpod Provider 汇总
│   ├── network/            Dio 封装与统一异常
│   ├── router/             go_router 路由表
│   ├── storage/            本地存储（令牌、设备 ID）
│   └── theme/              主题与设计令牌
├── features/               按业务域分目录，每个目录自带页面与状态
└── shared/widgets/         跨业务复用组件
```

分层原则：**feature 内可以依赖 core，core 不允许依赖任何 feature**。
跨 feature 复用先下沉到 shared，禁止互相 import。

## 运行

```bash
flutter pub get

# 默认连 http://127.0.0.1:8080（见 core/config/app_env.dart）
flutter run

# 真机 / 局域网调试时覆盖后端地址
flutter run --dart-define=API_BASE_URL=http://192.168.1.10:8080
```

## P0 的验证点

启动后「附近」页顶部会显示一张连通性卡片：

- 转圈 → 正在请求 `/v1/ping`
- 绿色勾 + 服务名/版本/服务器时间 → 前后端链路打通
- 红色感叹号 + 错误详情 + 重试按钮 → 后端未启动或地址不对

下拉可重新探测。

## 已接入能力

- P1：手机号登录、双 Token 自动续期、实名状态、IM UserSig 冷启动容错恢复；
- P2：资料编辑、头像上传、他人主页；
- P3：会话列表、消息历史、文本发送、已读、撤回、腾讯 IM 长连接和 Android 系统通知；IM 未配置时自动回退到 10 秒轮询。
- P6：腾讯 TRTC 语音/视频进房、麦克风与扬声器控制、远端画面，以及来电检测和锁屏来电通知。

Android 进程存活时，IM 长连接或 REST 轮询会生成本地系统通知。若要在 App 被系统彻底结束后仍能收到离线推送，还需在腾讯云 IM 控制台配置应用商店/厂商推送证书，并将控制台下载的 `timpush-configs.json` 接入 Android 平台壳。

## 后续接入（按阶段）

| 阶段 | 会新增的依赖 | 说明 |
|---|---|---|
| P1.5 | `flutter_secure_storage` | 用系统安全区替换 SharedPreferences 存令牌 |
| P3.5（上线前） | `tencent_cloud_chat_push` + `timpush-configs.json` | App 进程被结束后的厂商离线推送 |
| P7 | `geolocator`、`permission_handler` | 定位与权限 |
