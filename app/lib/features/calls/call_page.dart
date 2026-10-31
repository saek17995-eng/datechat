import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../profile/profile_image.dart';
import 'call_controller.dart';
import 'data/call_models.dart';
import 'data/rtc_bridge.dart';

/// 通话页（语音/视频共用）。
///
/// 页面只做三件事：展示状态、收集操作、跟随服务端结论。
/// 计时、余额、已扣分钟全部来自服务端心跳回包，
/// 客户端自己的秒表仅用于界面动画感，绝不参与任何金额计算。
class CallPage extends ConsumerStatefulWidget {
  const CallPage({
    super.key,
    this.peerUid = 0,
    this.callNo = '',
    this.peerName = '',
    this.peerAvatar = '',
    this.isVideo = false,
    this.autoAccept = false,
  });

  /// 主叫场景：被叫用户 ID。被叫场景为 0。
  final int peerUid;

  /// 被叫场景：来电单号。
  final String callNo;
  final String peerName;
  final String peerAvatar;
  final bool isVideo;

  /// true 表示被叫从"来电"入口进入，页面初始化时自动接听。
  final bool autoAccept;

  @override
  ConsumerState<CallPage> createState() => _CallPageState();
}

class _CallPageState extends ConsumerState<CallPage> {
  bool _started = false;

  @override
  void initState() {
    super.initState();
    // 首帧结束后再发起/接听：避免在 build 期间改 provider 状态触发断言，
    // 也保证控制器已经被页面 watch 住，不会因为是 autoDispose 被提前回收。
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_started) return;
      _started = true;
      if (widget.autoAccept) {
        unawaited(_accept());
      } else {
        unawaited(_start());
      }
    });
  }

  Future<void> _start() async {
    await ref.read(callControllerProvider.notifier).start(
          calleeId: widget.peerUid,
          video: widget.isVideo,
          peerName: widget.peerName,
          peerAvatar: widget.peerAvatar,
        );
  }

  void _hangup() {
    ref.read(callControllerProvider.notifier).hangup();
  }

  void _reject() {
    ref.read(callControllerProvider.notifier).reject();
  }

  Future<void> _accept() async {
    await ref.read(callControllerProvider.notifier).accept(
          widget.callNo,
          peerName: widget.peerName,
          peerAvatar: widget.peerAvatar,
          video: widget.isVideo,
        );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final state = ref.watch(callControllerProvider);
    final talking = state.phase == CallPhase.talking;
    final ringing = state.phase == CallPhase.ringing;
    final ended = state.phase == CallPhase.ended;

    return PopScope(
      // 通话中禁止用系统返回键离开：离开了心跳还在跑，用户会莫名被扣费。
      canPop: ended,
      child: Scaffold(
        backgroundColor: theme.colorScheme.surface,
        appBar: AppBar(
          backgroundColor: Colors.transparent,
          title: Text(widget.isVideo ? '视频通话' : '语音通话'),
        ),
        body: SafeArea(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(24, 12, 24, 32),
            child: Column(
              children: [
                CircleAvatar(
                  radius: 46,
                  backgroundImage: widget.peerAvatar.isNotEmpty
                      ? NetworkImage(ProfileImage.absUrl(widget.peerAvatar))
                      : null,
                  child: widget.peerAvatar.isEmpty
                      ? const Icon(Icons.person, size: 40)
                      : null,
                ),
                const SizedBox(height: 16),
                Text(
                  widget.peerName.isEmpty ? '对方' : widget.peerName,
                  style: theme.textTheme.titleLarge,
                ),
                const SizedBox(height: 8),
                _StatusLine(state: state),
                const SizedBox(height: 12),
                _EngineHint(ticket: state.ticket),
                const Spacer(),
                if (!ended)
                  Text(
                    state.durationText,
                    style: theme.textTheme.displaySmall?.copyWith(
                      fontWeight: FontWeight.w300,
                    ),
                  ),
                if (!ended) const SizedBox(height: 8),
                if (!ended)
                  Text(
                    '已计费 ${state.chargedMinutes} 分钟 · ${state.totalCoins} 金币',
                    style: theme.textTheme.bodyMedium?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                if (!ended)
                  Text(
                    '单价 ${state.pricePerMin} 金币/分钟 · 余额 ${state.balance}',
                    style: theme.textTheme.bodySmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                const Spacer(),
                if (ended) _BillSummary(state: state),
                const SizedBox(height: 16),
                if (ringing && !state.isCaller)
                  _IncomingActions(
                    onReject: _reject,
                    onAccept: _accept,
                  ),
                if (ringing && state.isCaller)
                  _SingleAction(
                    icon: Icons.call_end,
                    label: '取消呼叫',
                    filled: false,
                    onTap: _hangup,
                  ),
                if (talking)
                  _TalkingActions(
                    state: state,
                    onToggleMic: () =>
                        ref.read(callControllerProvider.notifier).toggleMic(),
                    onToggleSpeaker: () => ref
                        .read(callControllerProvider.notifier)
                        .toggleSpeaker(),
                    onHangup: _hangup,
                  ),
                if (ended)
                  SizedBox(
                    width: double.infinity,
                    height: 48,
                    child: FilledButton(
                      onPressed: () => context.pop(),
                      child: const Text('返回'),
                    ),
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _StatusLine extends StatelessWidget {
  const _StatusLine({required this.state});

  final CallState state;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final text = switch (state.phase) {
      CallPhase.idle || CallPhase.dialing => '正在呼叫…',
      CallPhase.ringing => state.isCaller ? '等待对方接听…' : '来电',
      CallPhase.talking => '通话中',
      CallPhase.ended => state.session?.endReasonText.isNotEmpty == true
          ? state.session!.endReasonText
          : '通话已结束',
    };
    return Text(
      text,
      style: theme.textTheme.bodyMedium?.copyWith(
        color: theme.colorScheme.onSurfaceVariant,
      ),
    );
  }
}

class _EngineHint extends StatelessWidget {
  const _EngineHint({required this.ticket});

  final CallTicket? ticket;

  @override
  Widget build(BuildContext context) {
    if (ticket == null) return const SizedBox.shrink();
    final bridge = RtcBridge.fromTicket(ticket!);
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(16),
      ),
      child: Text(
        bridge.hint,
        style: theme.textTheme.bodySmall,
      ),
    );
  }
}

class _BillSummary extends StatelessWidget {
  const _BillSummary({required this.state});

  final CallState state;

  @override
  Widget build(BuildContext context) {
    final session = state.session;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          children: [
            _Row(label: '通话时长', value: state.durationText),
            _Row(label: '计费分钟', value: '${state.chargedMinutes} 分钟'),
            _Row(label: '消耗金币', value: '${state.totalCoins}'),
            if (session != null && session.platformFee > 0)
              _Row(label: '其中平台服务费', value: '${session.platformFee}'),
          ],
        ),
      ),
    );
  }
}

class _Row extends StatelessWidget {
  const _Row({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [
          Text(label, style: theme.textTheme.bodyMedium),
          Text(value, style: theme.textTheme.titleMedium),
        ],
      ),
    );
  }
}

class _SingleAction extends StatelessWidget {
  const _SingleAction({
    required this.icon,
    required this.label,
    required this.onTap,
    this.filled = false,
  });

  final IconData icon;
  final String label;
  final VoidCallback onTap;
  final bool filled;

  @override
  Widget build(BuildContext context) {
    final child = Row(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [Icon(icon), const SizedBox(width: 8), Text(label)],
    );
    return SizedBox(
      width: double.infinity,
      height: 52,
      child: filled
          ? FilledButton(onPressed: onTap, child: child)
          : OutlinedButton(onPressed: onTap, child: child),
    );
  }
}

class _IncomingActions extends StatelessWidget {
  const _IncomingActions({required this.onReject, required this.onAccept});

  final VoidCallback onReject;
  final Future<void> Function() onAccept;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Expanded(
          child: _SingleAction(
            icon: Icons.call_end,
            label: '拒绝',
            onTap: onReject,
          ),
        ),
        const SizedBox(width: 16),
        Expanded(
          child: _SingleAction(
            icon: Icons.call,
            label: '接听',
            filled: true,
            onTap: () => onAccept(),
          ),
        ),
      ],
    );
  }
}

class _TalkingActions extends StatelessWidget {
  const _TalkingActions({
    required this.state,
    required this.onToggleMic,
    required this.onToggleSpeaker,
    required this.onHangup,
  });

  final CallState state;
  final VoidCallback onToggleMic;
  final VoidCallback onToggleSpeaker;
  final VoidCallback onHangup;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisAlignment: MainAxisAlignment.spaceEvenly,
      children: [
        _RoundButton(
          icon: state.micMuted ? Icons.mic_off : Icons.mic,
          label: state.micMuted ? '已静音' : '静音',
          onTap: onToggleMic,
        ),
        _RoundButton(
          icon: Icons.call_end,
          label: '挂断',
          color: Colors.red,
          onTap: onHangup,
        ),
        _RoundButton(
          icon: state.speakerOn ? Icons.volume_up : Icons.volume_off,
          label: '免提',
          onTap: onToggleSpeaker,
        ),
      ],
    );
  }
}

class _RoundButton extends StatelessWidget {
  const _RoundButton({
    required this.icon,
    required this.label,
    required this.onTap,
    this.color,
  });

  final IconData icon;
  final String label;
  final VoidCallback onTap;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final tint = color ?? theme.colorScheme.onSurface;
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        InkResponse(
          onTap: onTap,
          radius: 40,
          child: CircleAvatar(
            radius: 30,
            backgroundColor: theme.colorScheme.surfaceContainerHighest,
            child: Icon(icon, color: tint),
          ),
        ),
        const SizedBox(height: 6),
        Text(label, style: theme.textTheme.bodySmall),
      ],
    );
  }
}
