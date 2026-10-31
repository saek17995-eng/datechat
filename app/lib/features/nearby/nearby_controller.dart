import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/di/providers.dart';
import '../../core/network/api_exception.dart';
import '../../features/auth/auth_controller.dart';
import 'data/nearby_api.dart';
import 'location_source.dart';

/// 定位来源。真机接入 GPS 后在这里换成 DeviceLocationSource 即可，
/// 业务层（列表、筛选、上报）完全不用改。
final locationSourceProvider = Provider<LocationSource>((ref) {
  return const PinnedLocationSource();
});

final nearbyApiProvider = Provider<NearbyApi>((ref) {
  return NearbyApi(ref.watch(apiClientProvider));
});

/// 在线心跳。
///
/// 在线状态是"服务端 TTL + 客户端续约"：客户端不发心跳，两分钟后自动离线。
/// 间隔取 60 秒（TTL 的一半），留一次重试的余量，弱网丢一次包不会掉线。
///
/// 只在已登录时启动；未登录时返回但不建定时器，避免登出后还在打接口。
final heartbeatProvider = Provider<void>((ref) {
  // 未登录不启动：这里读的是 controller 状态，登录态变化会重建该 provider。
  final started = ref.watch(heartbeatGateProvider);
  if (!started) return;

  final api = ref.watch(nearbyApiProvider);
  unawaited(_beat(api));
  final timer = Timer.periodic(const Duration(seconds: 60), (_) => _beat(api));
  ref.onDispose(timer.cancel);
});

Future<void> _beat(NearbyApi api) async {
  try {
    await api.heartbeat();
  } catch (_) {
    // 心跳失败不打扰用户：下一次 60 秒后会重试，最坏结果是提前离线。
  }
}

/// 附近的人筛选条件。
class NearbyFilter {
  const NearbyFilter({
    this.gender = 0,
    this.ageMin = 0,
    this.ageMax = 0,
  });

  /// 0 不限 1 男 2 女
  final int gender;
  final int ageMin;
  final int ageMax;

  NearbyFilter copyWith({int? gender, int? ageMin, int? ageMax}) =>
      NearbyFilter(
        gender: gender ?? this.gender,
        ageMin: ageMin ?? this.ageMin,
        ageMax: ageMax ?? this.ageMax,
      );

  bool get isEmpty => gender == 0 && ageMin <= 0 && ageMax <= 0;
}

class NearbyState {
  const NearbyState({
    this.items = const [],
    this.total = 0,
    this.loading = false,
    this.error,
    this.filter = const NearbyFilter(),
    this.coords,
    this.fromDevice = false,
  });

  final List<NearbyUser> items;
  final int total;
  final bool loading;
  final String? error;

  /// 空态要区分"真的没人"和"被筛选条件筛没了"，所以 filter 也放进状态。
  final NearbyFilter filter;
  final Coords? coords;
  final bool fromDevice;

  NearbyState copyWith({
    List<NearbyUser>? items,
    int? total,
    bool? loading,
    Object? error = _sentinel,
    NearbyFilter? filter,
    Coords? coords,
    bool? fromDevice,
  }) =>
      NearbyState(
        items: items ?? this.items,
        total: total ?? this.total,
        loading: loading ?? this.loading,
        error: identical(error, _sentinel) ? this.error : error as String?,
        filter: filter ?? this.filter,
        coords: coords ?? this.coords,
        fromDevice: fromDevice ?? this.fromDevice,
      );
}

const Object _sentinel = Object();

/// 附近的人控制器。
///
/// 顺序固定：先取坐标 → 上报（服务端要最新的，否则列表按旧位置排）→ 再拉列表。
/// 上报失败不阻断拉列表：节流（30 秒内重复）本来就是预期行为。
class NearbyController extends StateNotifier<NearbyState> {
  NearbyController(this._ref) : super(const NearbyState());

  final Ref _ref;

  Future<void> refresh({bool relocate = true}) async {
    state = state.copyWith(loading: true, error: _sentinel);
    try {
      final source = _ref.read(locationSourceProvider);
      Coords? coords = state.coords;
      if (relocate || coords == null) {
        coords = await source.current();
      }
      if (coords == null) {
        state = state.copyWith(
          loading: false,
          error: '未获取到定位，请在系统设置里授权，或在下方手动设置坐标',
        );
        return;
      }
      final api = _ref.read(nearbyApiProvider);
      if (relocate) {
        // 上报失败只记不拦：被节流说明 30 秒内刚上报过，坐标足够新。
        try {
          await api.reportLocation(coords.lng, coords.lat);
        } catch (_) {}
      }
      final page = await api.nearby(
        lng: coords.lng,
        lat: coords.lat,
        gender: state.filter.gender,
        ageMin: state.filter.ageMin,
        ageMax: state.filter.ageMax,
      );
      state = state.copyWith(
        items: page.items,
        total: page.total,
        loading: false,
        error: _sentinel,
        coords: coords,
        fromDevice: source.isDevice,
      );
    } on ApiException catch (error) {
      state = state.copyWith(loading: false, error: error.message);
    } catch (error) {
      state = state.copyWith(loading: false, error: '$error');
    }
  }

  /// 改筛选条件并立即重拉。
  Future<void> setFilter(NearbyFilter filter) async {
    state = state.copyWith(filter: filter);
    await refresh(relocate: false);
  }

  /// 手动设置坐标（联调用）。
  Future<void> pin(Coords coords) async {
    final source = _ref.read(locationSourceProvider);
    if (source is PinnedLocationSource) {
      await source.save(coords);
    }
    state = state.copyWith(coords: coords);
    await refresh();
  }
}

final nearbyControllerProvider =
    StateNotifierProvider.autoDispose<NearbyController, NearbyState>((ref) {
  return NearbyController(ref);
});

/// 谁看过我：列表 + 未读数。
final visitsProvider = FutureProvider.autoDispose<VisitPage>((ref) async {
  return ref.watch(nearbyApiProvider).visits();
});

/// 未读访客数。单独拆一个 provider，"我的"页要显示红点但不需要整份列表。
final unreadVisitsProvider = Provider<int>((ref) {
  return ref.watch(visitsProvider).maybeWhen(
        data: (page) => page.unread,
        orElse: () => 0,
      );
});

/// 心跳开关：只有已登录才启动，登出后定时器随之取消。
final heartbeatGateProvider = Provider<bool>((ref) {
  return ref.watch(authControllerProvider
      .select((state) => state.status == AuthStatus.authenticated));
});
