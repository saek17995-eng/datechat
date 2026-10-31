import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/di/providers.dart';
import '../../core/network/api_exception.dart';
import '../../core/storage/token_store.dart';
import 'data/auth_api.dart';
import 'data/auth_models.dart';

/// 登录状态。
///
/// unknown 是冷启动时的过渡态：本地有令牌但还没校验，此时不应把用户踢去登录页，
/// 否则每次冷启动都会闪一下登录页。
enum AuthStatus { unknown, authenticated, unauthenticated }

class AuthState {
  const AuthState({
    required this.status,
    this.me,
    this.im = ImCredential.none,
    this.error,
  });

  const AuthState.unknown() : this(status: AuthStatus.unknown);

  const AuthState.unauthenticated() : this(status: AuthStatus.unauthenticated);

  final AuthStatus status;
  final MeInfo? me;
  final ImCredential im;

  /// 最近一次失败原因，供登录页展示；成功后清空。
  final String? error;

  bool get isAuthenticated => status == AuthStatus.authenticated;

  AuthState copyWith({
    AuthStatus? status,
    MeInfo? me,
    ImCredential? im,
    String? error,
    bool clearError = false,
  }) {
    return AuthState(
      status: status ?? this.status,
      me: me ?? this.me,
      im: im ?? this.im,
      error: clearError ? null : (error ?? this.error),
    );
  }
}

/// 账号接口。
final authApiProvider = Provider<AuthApi>((ref) {
  return AuthApi(ref.watch(apiClientProvider));
});

/// 全局登录状态。
final authControllerProvider =
    StateNotifierProvider<AuthController, AuthState>((ref) {
  return AuthController(
    ref.watch(authApiProvider),
    ref.watch(tokenStoreProvider),
  );
});

/// 登录态控制器。
///
/// 令牌的落盘由它负责，业务页面不直接碰 TokenStore，
/// 避免出现"页面改了令牌但状态没更新"的不一致。
class AuthController extends StateNotifier<AuthState> {
  AuthController(this._api, this._tokens) : super(const AuthState.unknown()) {
    _restore();
  }

  final AuthApi _api;
  final TokenStore _tokens;

  /// 冷启动恢复会话：本地有令牌就校验一次，失败即视为未登录。
  Future<void> _restore() async {
    if (!_tokens.isLoggedIn) {
      state = const AuthState.unauthenticated();
      return;
    }
    try {
      // me 与 userSig 并行发：两者互不依赖，串行会让冷启动多等一个 RTT。
      // userSig 单独容错——IM 没配置好也不该把用户踢下线。
      final meFuture = _api.me();
      final imFuture = _loadUserSig();
      state = AuthState(
        status: AuthStatus.authenticated,
        me: await meFuture,
        im: await imFuture,
      );
    } on ApiException catch (error) {
      // 只有鉴权类错误才清令牌；网络异常时应保留令牌，等下次启动再试，
      // 否则弱网下会把用户踢下线。
      if (error.isUnauthorized) {
        await _tokens.clear();
        state = const AuthState.unauthenticated();
      } else {
        state = AuthState(
          status: AuthStatus.unauthenticated,
          error: error.message,
        );
      }
    } catch (_) {
      state = const AuthState.unauthenticated();
    }
  }

  /// 拉取 IM 登录材料。失败一律降级成"未启用"，不影响登录态：
  /// 后端没接 IM 时这个接口必然报错，而聊天走的是后端 REST 门面，照样能用。
  Future<ImCredential> _loadUserSig() async {
    try {
      return await _api.userSig();
    } catch (_) {
      return ImCredential.none;
    }
  }

  /// 发送验证码，返回 dev 环境下的调试验证码（生产返回 null）。
  Future<String?> sendCode(String phone) => _api.sendCode(phone);

  /// 登录成功返回 true，失败把原因写进 state.error 并返回 false。
  Future<bool> login({required String phone, required String code}) async {
    try {
      final result = await _api.login(
        phone: phone,
        code: code,
        deviceId: _tokens.deviceId,
      );
      if (result.token.isEmpty) {
        state = state.copyWith(error: '登录返回的令牌为空，请稍后重试');
        return false;
      }
      await _tokens.saveTokens(
        accessToken: result.token.accessToken,
        refreshToken: result.token.refreshToken,
      );
      state = AuthState(
        status: AuthStatus.authenticated,
        me: result.me,
        im: result.im,
      );
      return true;
    } on ApiException catch (error) {
      state = state.copyWith(error: error.message);
      return false;
    }
  }

  /// 刷新当前用户信息（改资料、实名通过后调用）。
  Future<void> refreshMe() async {
    if (!state.isAuthenticated) return;
    try {
      final me = await _api.me();
      state = state.copyWith(me: me);
    } on ApiException {
      // 拉取失败不改变登录态，避免因为一次网络抖动被登出。
    }
  }

  /// 退出登录：先通知服务端吊销刷新态，再清本地。
  /// 服务端失败也要清本地，否则用户会觉得"点了没反应"。
  Future<void> logout() async {
    try {
      await _api.logout();
    } catch (_) {
      // 忽略：本地凭证必须清掉
    }
    await _tokens.clear();
    state = const AuthState.unauthenticated();
  }

  void clearError() {
    if (state.error != null) {
      state = state.copyWith(clearError: true);
    }
  }
}
