import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'data/wallet_models.dart';
import 'wallet_controller.dart';

/// 我的钱包。
///
/// 一屏三块：余额卡、充值档位、金币流水。
/// 所有金额与金币都来自服务端档位表，页面只做展示与下单触发，
/// 不参与任何金额计算——避免出现"客户端算出来 100 金币、服务端只发了 60"的分歧。
class WalletPage extends ConsumerStatefulWidget {
  const WalletPage({super.key});

  @override
  ConsumerState<WalletPage> createState() => _WalletPageState();
}

class _WalletPageState extends ConsumerState<WalletPage> {
  @override
  void initState() {
    super.initState();
    // 进入时记录余额，充值成功后用于提示"本次到账 N 金币"。
    final wallet =
        ref.read(walletProvider).maybeWhen(data: (w) => w, orElse: () => null);
    if (wallet != null) {
      ref
          .read(rechargeControllerProvider.notifier)
          .primeBalance(wallet.coinBalance);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final walletAsync = ref.watch(walletProvider);
    final packagesAsync = ref.watch(packagesProvider);
    final txAsync = ref.watch(walletTransactionsProvider);
    final recharge = ref.watch(rechargeControllerProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('我的钱包')),
      body: RefreshIndicator(
        onRefresh: () async {
          ref.invalidate(walletProvider);
          ref.invalidate(packagesProvider);
          ref.invalidate(walletTransactionsProvider);
        },
        child: ListView(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
          children: [
            walletAsync.when(
              loading: () => const SizedBox(
                height: 120,
                child: Center(child: CircularProgressIndicator()),
              ),
              error: (error, _) => _ErrorCard(
                text: '钱包加载失败：$error',
                onRetry: () => ref.invalidate(walletProvider),
              ),
              data: (wallet) => _BalanceCard(wallet: wallet),
            ),
            const SizedBox(height: 20),
            Text('充值档位', style: theme.textTheme.titleMedium),
            const SizedBox(height: 8),
            packagesAsync.when(
              loading: () => const Padding(
                padding: EdgeInsets.all(24),
                child: Center(child: CircularProgressIndicator()),
              ),
              error: (error, _) => _ErrorCard(
                text: '档位加载失败：$error',
                onRetry: () => ref.invalidate(packagesProvider),
              ),
              data: (packages) => packages.isEmpty
                  ? const _EmptyHint(text: '暂无可购买档位')
                  : _PackageGrid(
                      packages: packages,
                      busy: recharge.busy,
                      selectedId: recharge.packageId,
                      onBuy: (pkg) => _buy(context, ref, pkg),
                    ),
            ),
            const SizedBox(height: 20),
            Text('金币流水', style: theme.textTheme.titleMedium),
            const SizedBox(height: 8),
            txAsync.when(
              loading: () => const Padding(
                padding: EdgeInsets.all(24),
                child: Center(child: CircularProgressIndicator()),
              ),
              error: (error, _) => _ErrorCard(
                text: '流水加载失败：$error',
                onRetry: () => ref.invalidate(walletTransactionsProvider),
              ),
              data: (list) => list.isEmpty
                  ? const _EmptyHint(text: '还没有金币变动')
                  : Column(
                      children:
                          list.map((tx) => _TransactionTile(tx: tx)).toList(),
                    ),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _buy(
    BuildContext context,
    WidgetRef ref,
    RechargePackage pkg,
  ) async {
    final ok = await ref.read(rechargeControllerProvider.notifier).buy(pkg);
    if (!context.mounted) return;
    final state = ref.read(rechargeControllerProvider);
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(
          ok ? '充值成功，到账 ${state.coins} 金币' : (state.error ?? '充值失败'),
        ),
      ),
    );
    if (ok) ref.read(rechargeControllerProvider.notifier).reset();
  }
}

class _BalanceCard extends StatelessWidget {
  const _BalanceCard({required this.wallet});

  final Wallet wallet;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      clipBehavior: Clip.antiAlias,
      child: Padding(
        padding: const EdgeInsets.all(20),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('金币余额', style: theme.textTheme.bodyMedium),
            const SizedBox(height: 6),
            Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Text(
                  '${wallet.coinBalance}',
                  style: theme.textTheme.displaySmall?.copyWith(
                    fontWeight: FontWeight.bold,
                  ),
                ),
                const SizedBox(width: 6),
                Padding(
                  padding: const EdgeInsets.only(bottom: 6),
                  child: Text('金币', style: theme.textTheme.bodySmall),
                ),
              ],
            ),
            const SizedBox(height: 16),
            Row(
              children: [
                _StatItem(label: '累计充值', value: wallet.totalRecharge),
                _StatItem(label: '累计消费', value: wallet.totalConsume),
                _StatItem(label: '冻结中', value: wallet.frozenCoin),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _StatItem extends StatelessWidget {
  const _StatItem({required this.label, required this.value});

  final String label;
  final int value;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Expanded(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            '$value',
            style: theme.textTheme.titleMedium
                ?.copyWith(fontWeight: FontWeight.w600),
          ),
          const SizedBox(height: 2),
          Text(
            label,
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
    );
  }
}

class _PackageGrid extends StatelessWidget {
  const _PackageGrid({
    required this.packages,
    required this.busy,
    required this.selectedId,
    required this.onBuy,
  });

  final List<RechargePackage> packages;
  final bool busy;
  final int? selectedId;
  final ValueChanged<RechargePackage> onBuy;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Wrap(
      spacing: 12,
      runSpacing: 12,
      children: packages.map((pkg) {
        final selected = pkg.id == selectedId;
        return SizedBox(
          width: (MediaQuery.of(context).size.width - 44) / 2,
          child: OutlinedButton(
            onPressed: busy ? null : () => onBuy(pkg),
            style: OutlinedButton.styleFrom(
              padding: const EdgeInsets.symmetric(vertical: 14, horizontal: 12),
              side: BorderSide(
                color: selected
                    ? theme.colorScheme.primary
                    : theme.colorScheme.outlineVariant,
              ),
            ),
            child: Column(
              children: [
                Text(
                  '${pkg.totalCoins} 金币',
                  style: theme.textTheme.titleMedium
                      ?.copyWith(fontWeight: FontWeight.w600),
                ),
                if (pkg.bonusCoins > 0)
                  Padding(
                    padding: const EdgeInsets.only(top: 2),
                    child: Text(
                      '含赠送 ${pkg.bonusCoins}',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.primary,
                      ),
                    ),
                  ),
                const SizedBox(height: 6),
                Text('¥${pkg.priceText}', style: theme.textTheme.bodyLarge),
                if (busy && selected)
                  const Padding(
                    padding: EdgeInsets.only(top: 6),
                    child: SizedBox(
                      height: 14,
                      width: 14,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    ),
                  ),
              ],
            ),
          ),
        );
      }).toList(),
    );
  }
}

class _TransactionTile extends StatelessWidget {
  const _TransactionTile({required this.tx});

  final WalletTransaction tx;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final income = tx.isIncome;
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: CircleAvatar(
        backgroundColor: theme.colorScheme.surfaceContainerHighest,
        child: Icon(
          income ? Icons.add : Icons.remove,
          color: income ? Colors.green : theme.colorScheme.onSurfaceVariant,
        ),
      ),
      title: Text(tx.title),
      subtitle: Text(
        tx.remark.isEmpty ? tx.bizId : tx.remark,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      trailing: Text(
        '${income ? '+' : '-'}${tx.coins}',
        style: theme.textTheme.titleMedium?.copyWith(
          // 中国用户习惯：收益/涨用红，支出用中性色，这里收益红、消费默认色。
          color: income ? Colors.red : theme.colorScheme.onSurface,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

class _ErrorCard extends StatelessWidget {
  const _ErrorCard({required this.text, required this.onRetry});

  final String text;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Row(
          children: [
            Expanded(child: Text(text)),
            TextButton(onPressed: onRetry, child: const Text('重试')),
          ],
        ),
      ),
    );
  }
}

class _EmptyHint extends StatelessWidget {
  const _EmptyHint({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 24),
      child: Center(
        child: Text(
          text,
          style: theme.textTheme.bodyMedium?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
      ),
    );
  }
}
