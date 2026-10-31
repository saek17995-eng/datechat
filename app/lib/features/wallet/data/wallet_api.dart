import '../../../core/network/dio_client.dart';
import 'wallet_models.dart';

/// 钱包与充值接口。
///
/// 关键约束：**金币数量与金额永远由服务端决定**。
/// 客户端只提交 package_id，绝不传价格——否则改个包就能 1 分钱买一万金币。
class WalletApi {
  WalletApi(this._client);

  final DioClient _client;

  Future<Wallet> me() async {
    return Wallet.fromJson(await _client.get('/v1/wallet'));
  }

  Future<List<RechargePackage>> packages() async {
    final data = await _client.get('/v1/wallet/packages');
    final list = data['list'];
    if (list is! List) return const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(RechargePackage.fromJson)
        .toList();
  }

  Future<List<WalletTransaction>> transactions({int page = 1, int size = 20}) {
    return _transactions('/v1/wallet/transactions?page=$page&size=$size');
  }

  Future<List<WalletTransaction>> _transactions(String path) async {
    final data = await _client.get(path);
    final list = data['list'];
    if (list is! List) return const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(WalletTransaction.fromJson)
        .toList();
  }

  /// 下单。返回支付参数：真实通道唤起微信，模拟通道由客户端直接确认。
  Future<RechargeOrder> createOrder(int packageId) async {
    final data = await _client
        .post('/v1/wallet/recharge', body: {'package_id': packageId});
    return RechargeOrder.fromJson(data);
  }

  /// 主动查单补单：用户付了钱但没收到回调时的兜底。
  Future<int> syncOrder(String orderNo) async {
    final data = await _client.post('/v1/wallet/recharge/$orderNo/sync');
    return asInt(data['status']);
  }

  /// 本地模拟支付成功。仅 dev/沙箱可用，生产服务端会直接拒绝。
  Future<void> mockPaid(String orderNo) async {
    await _client.post('/v1/wallet/recharge/$orderNo/mock-paid');
  }
}
