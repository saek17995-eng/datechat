import 'bootstrap.dart';

/// 入口只做一件事：交给 bootstrap 完成依赖装配后启动 App。
/// 具体职责（初始化存储、注入 Provider、runApp）都在 bootstrap.dart 里。
void main() => bootstrap();
