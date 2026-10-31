import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'avatar_picker.dart';
import 'data/profile_models.dart';
import 'profile_controller.dart';
import 'profile_image.dart';

/// 资料编辑页。
///
/// 表单初值来自 myProfileProvider，保存时整份提交（所有字段都有当前值，
/// 语义上等价于"确认后的完整资料"）。服务端按"传了才改"处理，
/// 未变化的字段即便重复写入也不会触发审核记录。
class EditProfilePage extends ConsumerStatefulWidget {
  const EditProfilePage({super.key});

  @override
  ConsumerState<EditProfilePage> createState() => _EditProfilePageState();
}

class _EditProfilePageState extends ConsumerState<EditProfilePage> {
  final _formKey = GlobalKey<FormState>();
  final _nickname = TextEditingController();
  final _job = TextEditingController();
  final _bio = TextEditingController();
  final _height = TextEditingController();

  int _gender = 0;
  DateTime? _birthday;
  String _cityCode = '';
  List<String> _tags = const [];
  bool _initialized = false;

  @override
  void dispose() {
    _nickname.dispose();
    _job.dispose();
    _bio.dispose();
    _height.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final profileAsync = ref.watch(myProfileProvider);
    final editor = ref.watch(profileEditorProvider);

    return Scaffold(
      appBar: AppBar(
        title: const Text('编辑资料'),
        actions: [
          TextButton(
            onPressed: editor.saving ? null : _submit,
            child: editor.saving
                ? const SizedBox(
                    width: 16,
                    height: 16,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Text('保存'),
          ),
        ],
      ),
      body: profileAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (error, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text('资料加载失败：$error'),
                const SizedBox(height: 12),
                FilledButton(
                  onPressed: () => ref.invalidate(myProfileProvider),
                  child: const Text('重试'),
                ),
              ],
            ),
          ),
        ),
        data: (profile) {
          _initFrom(profile);
          return Form(
            key: _formKey,
            child: ListView(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 32),
              children: [
                Center(child: _AvatarField(profile: profile, ref: ref)),
                const SizedBox(height: 24),
                TextFormField(
                  controller: _nickname,
                  decoration: const InputDecoration(
                    labelText: '昵称',
                    counterText: '',
                  ),
                  maxLength: 16,
                  validator: (value) {
                    final text = value?.trim() ?? '';
                    if (text.isEmpty) return '昵称不能为空';
                    return null;
                  },
                ),
                const SizedBox(height: 12),
                _GenderField(
                  value: _gender,
                  onChanged: (value) => setState(() => _gender = value),
                ),
                const SizedBox(height: 12),
                _BirthdayField(
                  value: _birthday,
                  onChanged: (value) => setState(() => _birthday = value),
                ),
                const SizedBox(height: 12),
                _CityField(
                  value: _cityCode,
                  onChanged: (value) => setState(() => _cityCode = value),
                ),
                const SizedBox(height: 12),
                TextFormField(
                  controller: _height,
                  decoration: const InputDecoration(
                    labelText: '身高（cm）',
                    hintText: '80 ~ 250',
                  ),
                  keyboardType: TextInputType.number,
                  validator: (value) {
                    final text = value?.trim() ?? '';
                    if (text.isEmpty) return null;
                    final height = int.tryParse(text);
                    if (height == null) return '请输入数字';
                    if (height < 80 || height > 250) return '身高需在 80~250 之间';
                    return null;
                  },
                ),
                const SizedBox(height: 12),
                TextFormField(
                  controller: _job,
                  decoration: const InputDecoration(labelText: '职业'),
                  maxLength: 32,
                ),
                const SizedBox(height: 12),
                TextFormField(
                  controller: _bio,
                  decoration: const InputDecoration(
                    labelText: '个人签名',
                    alignLabelWithHint: true,
                  ),
                  maxLength: 200,
                  maxLines: 3,
                ),
                const SizedBox(height: 12),
                _TagsField(
                  tags: _tags,
                  onChanged: (value) => setState(() => _tags = value),
                ),
                if (editor.error != null) ...[
                  const SizedBox(height: 16),
                  Text(
                    editor.error!,
                    style: theme.textTheme.bodyMedium?.copyWith(
                      color: theme.colorScheme.error,
                    ),
                  ),
                ],
                const SizedBox(height: 24),
                SizedBox(
                  height: 48,
                  child: FilledButton(
                    onPressed: editor.saving ? null : _submit,
                    child: editor.saving
                        ? const SizedBox(
                            width: 20,
                            height: 20,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Text('保存资料'),
                  ),
                ),
              ],
            ),
          );
        },
      ),
    );
  }

  /// 只在第一次拿到数据时回填表单。
  /// 放在 build 里而不是 initState，是因为数据来自异步 provider；
  /// 用 _initialized 挡住后续刷新，避免用户正在输入时被服务端返回值覆盖。
  void _initFrom(ProfileInfo profile) {
    if (_initialized) return;
    _initialized = true;
    _nickname.text = profile.nickname;
    _job.text = profile.job;
    _bio.text = profile.bio;
    _height.text = profile.height > 0 ? profile.height.toString() : '';
    _gender = profile.gender;
    _cityCode = profile.cityCode;
    _tags = profile.tags;
    if (profile.birthday.isNotEmpty) {
      _birthday = DateTime.tryParse(profile.birthday);
    }
  }

  Future<void> _submit() async {
    FocusScope.of(context).unfocus();
    if (!(_formKey.currentState?.validate() ?? false)) return;

    final height = int.tryParse(_height.text.trim()) ?? 0;
    final ok = await ref.read(profileEditorProvider.notifier).save(
          ProfilePatch(
            nickname: _nickname.text.trim(),
            gender: _gender,
            birthday: _birthday == null
                ? null
                : '${_birthday!.year.toString().padLeft(4, '0')}-'
                    '${_birthday!.month.toString().padLeft(2, '0')}-'
                    '${_birthday!.day.toString().padLeft(2, '0')}',
            cityCode: _cityCode,
            height: height,
            job: _job.text.trim(),
            bio: _bio.text.trim(),
            tags: _tags,
          ),
        );
    if (!mounted || !ok) return;
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('资料已保存')),
    );
    Navigator.of(context).pop();
  }
}

class _AvatarField extends StatelessWidget {
  const _AvatarField({required this.profile, required this.ref});

  final ProfileInfo profile;
  final WidgetRef ref;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return InkWell(
      borderRadius: BorderRadius.circular(48),
      onTap: () => showAvatarPickerSheet(context, ref),
      child: Stack(
        children: [
          CircleAvatar(
            radius: 48,
            backgroundImage: profile.hasAvatar
                ? ProfileImage.provider(profile.avatar)
                : null,
            child: profile.hasAvatar
                ? null
                : Text(
                    profile.nickname.isEmpty
                        ? '?'
                        : profile.nickname.substring(0, 1),
                    style: theme.textTheme.headlineMedium,
                  ),
          ),
          Positioned(
            right: 0,
            bottom: 0,
            child: CircleAvatar(
              radius: 16,
              backgroundColor: theme.colorScheme.primary,
              child: Icon(
                Icons.camera_alt,
                size: 18,
                color: theme.colorScheme.onPrimary,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _GenderField extends StatelessWidget {
  const _GenderField({required this.value, required this.onChanged});

  final int value;
  final ValueChanged<int> onChanged;

  @override
  Widget build(BuildContext context) {
    return SegmentedButton<int>(
      segments: const [
        ButtonSegment(value: 0, label: Text('保密')),
        ButtonSegment(value: 1, label: Text('男')),
        ButtonSegment(value: 2, label: Text('女')),
      ],
      selected: {value},
      onSelectionChanged: (selection) => onChanged(selection.first),
    );
  }
}

class _BirthdayField extends StatelessWidget {
  const _BirthdayField({required this.value, required this.onChanged});

  final DateTime? value;
  final ValueChanged<DateTime?> onChanged;

  @override
  Widget build(BuildContext context) {
    final now = DateTime.now();
    final text = value == null
        ? '未填写'
        : '${value!.year}-${value!.month.toString().padLeft(2, '0')}-'
            '${value!.day.toString().padLeft(2, '0')}';
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: const Icon(Icons.cake_outlined),
      title: const Text('生日'),
      subtitle: Text(text),
      trailing: const Icon(Icons.chevron_right),
      onTap: () async {
        final picked = await showDatePicker(
          context: context,
          initialDate: value ?? DateTime(now.year - 22, now.month, now.day),
          firstDate: DateTime(now.year - 100),
          lastDate: DateTime(now.year - 18, now.month, now.day),
        );
        if (picked != null) onChanged(picked);
      },
    );
  }
}

class _CityField extends ConsumerWidget {
  const _CityField({required this.value, required this.onChanged});

  final String value;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final citiesAsync = ref.watch(citiesProvider);
    return citiesAsync.when(
      loading: () => const LinearProgressIndicator(),
      error: (error, _) => Text('城市加载失败：$error'),
      data: (cities) => DropdownButtonFormField<String>(
        initialValue: value.isEmpty ? null : value,
        decoration: const InputDecoration(labelText: '常驻城市'),
        items: [
          const DropdownMenuItem<String>(value: '', child: Text('未填写')),
          ...cities.map(
            (city) => DropdownMenuItem<String>(
              value: city.code,
              child: Text(city.label),
            ),
          ),
        ],
        onChanged: (code) => onChanged(code ?? ''),
      ),
    );
  }
}

class _TagsField extends StatefulWidget {
  const _TagsField({required this.tags, required this.onChanged});

  final List<String> tags;
  final ValueChanged<List<String>> onChanged;

  @override
  State<_TagsField> createState() => _TagsFieldState();
}

class _TagsFieldState extends State<_TagsField> {
  final _controller = TextEditingController();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text('标签（最多 6 个）', style: theme.textTheme.titleSmall),
        const SizedBox(height: 8),
        Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            for (final tag in widget.tags)
              InputChip(
                label: Text(tag),
                onDeleted: () {
                  final next = List<String>.of(widget.tags)..remove(tag);
                  widget.onChanged(next);
                },
              ),
          ],
        ),
        const SizedBox(height: 8),
        TextField(
          controller: _controller,
          decoration: InputDecoration(
            hintText: '输入标签后回车',
            suffixIcon: IconButton(
              icon: const Icon(Icons.add),
              onPressed: _add,
            ),
          ),
          onSubmitted: (_) => _add(),
        ),
      ],
    );
  }

  void _add() {
    final text = _controller.text.trim();
    if (text.isEmpty) return;
    if (widget.tags.length >= 6) return;
    if (widget.tags.contains(text)) {
      _controller.clear();
      return;
    }
    widget.onChanged([...widget.tags, text]);
    _controller.clear();
  }
}
