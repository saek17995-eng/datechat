import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../core/network/api_exception.dart';
import 'auth_controller.dart';

/// 手机号 + 验证码登录页。
///
/// 产品取舍：首期不做密码登录与第三方登录，验证码是唯一入口——
/// 少一个"找回密码"链路，也少一类"撞库"风险。
class LoginPage extends ConsumerStatefulWidget {
  const LoginPage({super.key});

  @override
  ConsumerState<LoginPage> createState() => _LoginPageState();
}

class _LoginPageState extends ConsumerState<LoginPage> {
  final TextEditingController _phone = TextEditingController();
  final TextEditingController _code = TextEditingController();

  Timer? _timer;
  int _countdown = 0;
  bool _sending = false;
  bool _submitting = false;
  String? _hint;

  @override
  void dispose() {
    _timer?.cancel();
    _phone.dispose();
    _code.dispose();
    super.dispose();
  }

  bool get _phoneValid => RegExp(r'^1[3-9]\d{9}$').hasMatch(_phone.text.trim());

  Future<void> _sendCode() async {
    if (!_phoneValid) {
      setState(() => _hint = '请输入正确的手机号');
      return;
    }
    setState(() {
      _sending = true;
      _hint = null;
    });
    try {
      final debugCode = await ref
          .read(authControllerProvider.notifier)
          .sendCode(_phone.text.trim());
      if (!mounted) return;
      setState(() {
        _countdown = 60;
        _hint = debugCode == null ? '验证码已发送，请注意查收' : '验证码已发送（调试码：$debugCode）';
      });
      _startCountdown();
    } on ApiException catch (error) {
      if (mounted) setState(() => _hint = error.message);
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  void _startCountdown() {
    _timer?.cancel();
    _timer = Timer.periodic(const Duration(seconds: 1), (timer) {
      if (!mounted) {
        timer.cancel();
        return;
      }
      setState(() {
        _countdown--;
        if (_countdown <= 0) timer.cancel();
      });
    });
  }

  Future<void> _submit() async {
    if (!_phoneValid) {
      setState(() => _hint = '请输入正确的手机号');
      return;
    }
    if (_code.text.trim().isEmpty) {
      setState(() => _hint = '请输入验证码');
      return;
    }
    FocusScope.of(context).unfocus();
    setState(() {
      _submitting = true;
      _hint = null;
    });
    final ok = await ref.read(authControllerProvider.notifier).login(
          phone: _phone.text.trim(),
          code: _code.text.trim(),
        );
    if (!mounted) return;
    setState(() => _submitting = false);
    if (!ok) {
      final error = ref.read(authControllerProvider).error;
      setState(() => _hint = error ?? '登录失败，请稍后重试');
    }
    // 成功后不需要手动跳转：登录态变化会触发路由 redirect。
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final canSend = _countdown == 0 && !_sending;

    return Scaffold(
      body: SafeArea(
        child: SingleChildScrollView(
          padding: const EdgeInsets.symmetric(horizontal: 28, vertical: 40),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const SizedBox(height: 24),
              Text('欢迎来到 DateChat', style: theme.textTheme.headlineSmall),
              const SizedBox(height: 8),
              Text(
                '手机号验证码登录，未注册将自动创建账号',
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
              const SizedBox(height: 36),
              TextField(
                controller: _phone,
                keyboardType: TextInputType.phone,
                maxLength: 11,
                inputFormatters: [FilteringTextInputFormatter.digitsOnly],
                decoration: const InputDecoration(
                  labelText: '手机号',
                  counterText: '',
                  prefixIcon: Icon(Icons.phone_iphone),
                  border: OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 16),
              Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: _code,
                      keyboardType: TextInputType.number,
                      maxLength: 6,
                      inputFormatters: [FilteringTextInputFormatter.digitsOnly],
                      decoration: const InputDecoration(
                        labelText: '验证码',
                        counterText: '',
                        prefixIcon: Icon(Icons.lock_outline),
                        border: OutlineInputBorder(),
                      ),
                    ),
                  ),
                  const SizedBox(width: 12),
                  SizedBox(
                    height: 56,
                    width: 116,
                    child: OutlinedButton(
                      onPressed: canSend ? _sendCode : null,
                      child: Text(
                        _countdown > 0 ? '${_countdown}s' : '获取验证码',
                        textAlign: TextAlign.center,
                      ),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 12),
              if (_hint != null)
                Text(
                  _hint!,
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.colorScheme.error,
                  ),
                ),
              const SizedBox(height: 24),
              SizedBox(
                height: 52,
                width: double.infinity,
                child: FilledButton(
                  onPressed: _submitting ? null : _submit,
                  child: _submitting
                      ? const SizedBox(
                          width: 20,
                          height: 20,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Text('登录 / 注册'),
                ),
              ),
              const SizedBox(height: 20),
              Text(
                '登录即表示同意《用户协议》与《隐私政策》。'
                '我们仅收集提供服务所必需的信息，手机号加密存储。',
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
