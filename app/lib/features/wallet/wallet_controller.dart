import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/di/providers.dart';
import '../../core/network/api_exception.dart';
import 'data/wallet_api.dart';
import 'data/wallet_models.dart';

final walletApiProvider = Provider<WalletApi>((ref) {
  return WalletApi(ref.watch(apiClientProvider));
});

/// 钱包概览。autoDispose：退出登录后不留驻。
final walletProvider = FutureProvider.autoDispose<Wallet>((ref) async {
  return ref.watch(walletApiProvider).me();
});

/// 充值档位。
final packagesProvider =
    FutureProvider.autoDispose<List<RechargePackage>>((ref) async {
  return ref.watch(walletApiProvider).packages();
});

/// 金币流水。
final walletTransactionsProvider =
    FutureProvider.autoDispose<List<WalletTransaction>>((ref) async {
  return ref.watch(walletApiProvider).transactions();
});

/// 充值状态机。
///
/// 三个阶段：idle → ordering → paying → done/failed。
/// 拆开而不是一个 loading 布尔，是为了让按钮在不同阶段显示不同文案，
/// 也避免用户连点产生多笔订单（真实支付下这是重复扣款）。
enum RechargeStage { idle, ordering, paying, done, failed }

class RechargeState {
  const RechargeState({
    this.stage = RechargeStage.idle,
    this.packageId,
    this.orderNo,
    this.coins = 0,
    this.error,
  });

  final RechargeStage stage;
  final int? packageId;
  final String? orderNo;
  final int coins;
  final String? error;

  bool get busy =>
      stage == RechargeStage.ordering || stage == RechargeStage.paying;

  RechargeState copyWith({
    RechargeStage? stage,
    int? packageId,
    String? orderNo,
    int? coins,
    String? error,
    bool clearError = false,
  }) {
    return RechargeState(
      stage: stage ?? this.stage,
      packageId: packageId ?? this.packageId,
      orderNo: orderNo ?? this.orderNo,
      coins: coins ?? this.coins,
      error: clearError ? null : (error ?? this.error),
    );
  }
}

/// 充值控制器。
///
/// 支付通道的分支只在这里判断一次：
///   - 真实通道（微信）返回 pay_params，交给支付 SDK 唤起；
///   - 沙箱通道返回 mock=true，直接调服务端"模拟支付"推进订单。
/// 页面层不需要知道当前跑的是哪种通道。
class RechargeController extends StateNotifier<RechargeState> {
  RechargeController(this._ref) : super(const RechargeState());

  final Ref _ref;
  int _balance = 0;

  /// 记录进入充值前的余额，成功后用于展示"本次到账 N 金币"。
  void primeBalance(int balance) => _balance = balance;

  int get lastBalanceBefore => _balance;

  Future<bool> buy(RechargePackage pkg) async {
    if (state.busy) return false;
    state = state.copyWith(
      stage: RechargeStage.ordering,
      packageId: pkg.id,
      clearError: true,
    );
    try {
      final order = await _ref.read(walletApiProvider).createOrder(pkg.id);
      if (order.isMock) {
        state =
            state.copyWith(stage: RechargeStage.paying, orderNo: order.orderNo);
        await _ref.read(walletApiProvider).mockPaid(order.orderNo);
      } else {
        // 真实通道：唤起微信支付后由回调/查单推进订单状态。
        // 这里只同步一次查单结果，最终结果以服务端为准。
        state =
            state.copyWith(stage: RechargeStage.paying, orderNo: order.orderNo);
        await _ref.read(walletApiProvider).syncOrder(order.orderNo);
      }
      state = state.copyWith(stage: RechargeStage.done, coins: pkg.totalCoins);
      _refresh();
      return true;
    } on ApiException catch (error) {
      state = state.copyWith(
        stage: RechargeStage.failed,
        error: error.message,
      );
      return false;
    } catch (_) {
      state = state.copyWith(
        stage: RechargeStage.failed,
        error: '网络异常，请稍后重试',
      );
      return false;
    }
  }

  void reset() => state = const RechargeState();

  void _refresh() {
    _ref.invalidate(walletProvider);
    _ref.invalidate(walletTransactionsProvider);
  }
}

final rechargeControllerProvider =
    StateNotifierProvider.autoDispose<RechargeController, RechargeState>((ref) {
  return RechargeController(ref);
});
