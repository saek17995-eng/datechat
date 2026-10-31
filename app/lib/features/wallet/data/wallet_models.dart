// 钱包领域模型：余额、充值档位、金币流水、充值订单。
//
// 只做 JSON → 对象，不做任何业务判断。
// 金额一律以「金币」为单位，分（fen）只出现在订单里，避免单位混用。

int asInt(Object? value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  return int.tryParse(value?.toString() ?? '') ?? 0;
}

/// 我的钱包。
class Wallet {
  const Wallet({
    required this.coinBalance,
    required this.frozenCoin,
    required this.totalRecharge,
    required this.totalConsume,
  });

  final int coinBalance;
  final int frozenCoin;
  final int totalRecharge;
  final int totalConsume;

  factory Wallet.fromJson(Map<String, dynamic> json) {
    return Wallet(
      coinBalance: asInt(json['coin_balance']),
      frozenCoin: asInt(json['frozen_coin']),
      totalRecharge: asInt(json['total_recharge']),
      totalConsume: asInt(json['total_consume']),
    );
  }
}

/// 充值档位。金额与金币都由服务端定义，客户端只展示。
class RechargePackage {
  const RechargePackage({
    required this.id,
    required this.coins,
    required this.bonusCoins,
    required this.priceFen,
  });

  final int id;
  final int coins;
  final int bonusCoins;
  final int priceFen;

  /// 到账金币（含赠送）。
  int get totalCoins => coins + bonusCoins;

  /// 售价（元）。
  String get priceText => (priceFen / 100).toStringAsFixed(2);

  factory RechargePackage.fromJson(Map<String, dynamic> json) {
    return RechargePackage(
      id: asInt(json['id']),
      coins: asInt(json['coins']),
      bonusCoins: asInt(json['bonus_coins']),
      priceFen: asInt(json['price_fen']),
    );
  }
}

/// 金币流水。账本只读，任何展示都不应本地修改它。
class WalletTransaction {
  const WalletTransaction({
    required this.bizType,
    required this.bizId,
    required this.direction,
    required this.coins,
    required this.balanceAfter,
    required this.remark,
  });

  /// 1充值 2通话消费 3退款 4活动奖励 5人工调整 6通话收益
  final int bizType;
  final String bizId;

  /// 1收入 2支出
  final int direction;
  final int coins;
  final int balanceAfter;
  final String remark;

  bool get isIncome => direction == 1;

  String get title {
    switch (bizType) {
      case 1:
        return '充值';
      case 2:
        return '通话消费';
      case 3:
        return '退款';
      case 4:
        return '活动奖励';
      case 5:
        return '人工调整';
      case 6:
        return '通话收益';
      default:
        return '其他';
    }
  }

  factory WalletTransaction.fromJson(Map<String, dynamic> json) {
    return WalletTransaction(
      bizType: asInt(json['biz_type']),
      bizId: (json['biz_id'] ?? '').toString(),
      direction: asInt(json['direction']),
      coins: asInt(json['coins']),
      balanceAfter: asInt(json['balance_after']),
      remark: (json['remark'] ?? '').toString(),
    );
  }
}

/// 充值下单结果。
class RechargeOrder {
  const RechargeOrder({
    required this.orderNo,
    required this.amountFen,
    required this.coins,
    required this.status,
    required this.payParams,
  });

  final String orderNo;
  final int amountFen;
  final int coins;

  /// 0待支付 1支付中 2已支付 3已失败 4已退款 5已关闭
  final int status;

  /// 支付参数。真实微信通道返回唤起 App 支付所需字段；
  /// 本地模拟通道返回 {mock: true}，客户端据此走"模拟支付"直连。
  final Map<String, dynamic> payParams;

  bool get isMock => payParams['mock'] == true;

  factory RechargeOrder.fromJson(Map<String, dynamic> json) {
    final params = json['pay_params'];
    return RechargeOrder(
      orderNo: (json['order_no'] ?? '').toString(),
      amountFen: asInt(json['amount_fen']),
      coins: asInt(json['coins']),
      status: asInt(json['status']),
      payParams: params is Map<String, dynamic>
          ? params
          : (params is Map ? Map<String, dynamic>.from(params) : const {}),
    );
  }
}
