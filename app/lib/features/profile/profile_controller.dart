import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../auth/auth_controller.dart';
import '../../core/di/providers.dart';
import '../../core/network/api_exception.dart';
import 'data/profile_api.dart';
import 'data/profile_models.dart';
import 'data/report_api.dart';

/// 资料接口。
final profileApiProvider = Provider<ProfileApi>((ref) {
  return ProfileApi(ref.watch(apiClientProvider));
});

/// 关系与内容安全接口（举报、拉黑）。
final safetyApiProvider = Provider<SafetyApi>((ref) {
  return SafetyApi(ref.watch(apiClientProvider));
});

/// 我的资料。autoDispose：退出登录或离开页面后不驻留内存。
final myProfileProvider = FutureProvider.autoDispose<ProfileInfo>((ref) async {
  return ref.watch(profileApiProvider).myProfile();
});

/// 城市字典。资料编辑页的城市选择器用，进程内缓存一次即可。
final citiesProvider = FutureProvider.autoDispose<List<CityItem>>((ref) async {
  return ref.watch(profileApiProvider).cities();
});

/// 他人主页。family 按 uid 分别缓存。
final userProfileProvider =
    FutureProvider.autoDispose.family<PublicUser, int>((ref, uid) async {
  return ref.watch(profileApiProvider).userProfile(uid);
});

/// 我的黑名单。autoDispose：离开黑名单页后不驻留内存。
final blacklistProvider =
    FutureProvider.autoDispose<BlacklistPage>((ref) async {
  return ref.watch(safetyApiProvider).blacklist();
});

/// 资料编辑状态。
class ProfileEditorState {
  const ProfileEditorState({this.saving = false, this.error});

  final bool saving;

  /// 最近一次失败原因。服务端内容审核被拦截时，文案直接展示给用户。
  final String? error;

  ProfileEditorState copyWith(
      {bool? saving, String? error, bool clearError = false}) {
    return ProfileEditorState(
      saving: saving ?? this.saving,
      error: clearError ? null : (error ?? this.error),
    );
  }
}

/// 资料编辑控制器。
///
/// 保存成功后同时失效两处缓存：
///   1. myProfileProvider（资料页）；
///   2. AuthController 里的 MeInfo（「我的」页头部、其他页面用到的昵称头像）。
/// 只刷一处就会出现"改完头像，我的页还是旧的"。
final profileEditorProvider =
    StateNotifierProvider<ProfileEditor, ProfileEditorState>((ref) {
  return ProfileEditor(ref);
});

class ProfileEditor extends StateNotifier<ProfileEditorState> {
  ProfileEditor(this._ref) : super(const ProfileEditorState());

  final Ref _ref;

  /// 保存资料。成功返回 true；失败把服务端文案写进 state.error。
  Future<bool> save(ProfilePatch patch) async {
    if (patch.isEmpty) return true;
    state = state.copyWith(saving: true, clearError: true);
    try {
      await _ref.read(profileApiProvider).updateProfile(patch);
      _invalidate();
      state = const ProfileEditorState();
      return true;
    } on ApiException catch (error) {
      state = ProfileEditorState(error: error.message);
      return false;
    } catch (_) {
      state = const ProfileEditorState(error: '网络异常，请稍后重试');
      return false;
    }
  }

  /// 上传并设置为头像。
  Future<bool> uploadAvatar({
    required List<int> bytes,
    required String filename,
  }) async {
    state = state.copyWith(saving: true, clearError: true);
    try {
      final api = _ref.read(profileApiProvider);
      final file = await api.uploadBytes(bytes: bytes, filename: filename);
      if (file.rejected) {
        state = const ProfileEditorState(error: '图片未通过审核，请更换后重试');
        return false;
      }
      await api.setAvatar(file.url);
      _invalidate();
      state = const ProfileEditorState();
      return true;
    } on ApiException catch (error) {
      state = ProfileEditorState(error: error.message);
      return false;
    } catch (_) {
      state = const ProfileEditorState(error: '上传失败，请稍后重试');
      return false;
    }
  }

  /// 直接用图片链接设置头像（联调用；服务端只接受本站上传过的地址）。
  Future<bool> setAvatarByUrl(String url) async {
    state = state.copyWith(saving: true, clearError: true);
    try {
      await _ref.read(profileApiProvider).setAvatar(url);
      _invalidate();
      state = const ProfileEditorState();
      return true;
    } on ApiException catch (error) {
      state = ProfileEditorState(error: error.message);
      return false;
    } catch (_) {
      state = const ProfileEditorState(error: '设置头像失败，请稍后重试');
      return false;
    }
  }

  void clearError() {
    if (state.error != null) {
      state = state.copyWith(clearError: true);
    }
  }

  void _invalidate() {
    _ref.invalidate(myProfileProvider);
    _ref.read(authControllerProvider.notifier).refreshMe();
  }
}
