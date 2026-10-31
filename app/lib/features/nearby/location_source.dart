import 'package:shared_preferences/shared_preferences.dart';

/// 坐标。
class Coords {
  const Coords({required this.lng, required this.lat});

  final double lng;
  final double lat;

  bool get valid =>
      lng >= -180 &&
      lng <= 180 &&
      lat >= -90 &&
      lat <= 90 &&
      (lng != 0 || lat != 0);

  @override
  String toString() => '${lng.toStringAsFixed(4)}, ${lat.toStringAsFixed(4)}';
}

/// 定位来源。
///
/// 与 RtcBridge 同样的思路：把"业务"和"原生能力"解耦。
/// 真实的 GPS 需要 geolocator 插件 + Android/iOS 的定位权限与隐私描述，
/// 那是接入期的工作；在那之前用 [PinnedLocationSource] 也能把
/// 「上报坐标 → GEO 检索 → 距离排序 → 在线状态」整条链路跑通并验收。
abstract class LocationSource {
  /// 取当前坐标；拿不到返回 null（调用方应提示而不是传 0,0 上去）。
  Future<Coords?> current();

  /// 是否来自真实设备定位。决定 UI 是否展示"当前定位"标识。
  bool get isDevice;
}

/// 固定坐标来源：坐标存在本地，页面上可手动修改。
///
/// 用途有两个：模拟器与未授权定位的调试；以及真机接入前的功能联调。
/// 注意它**不会**假装自己是 GPS——isDevice 返回 false，UI 上会明确提示。
class PinnedLocationSource implements LocationSource {
  const PinnedLocationSource();

  static const String _keyLng = 'nearby.pinned.lng';
  static const String _keyLat = 'nearby.pinned.lat';

  /// 默认值取北京：至少是一个合法坐标，不会让附近列表空得莫名其妙。
  static const Coords fallback = Coords(lng: 116.4074, lat: 39.9042);

  @override
  bool get isDevice => false;

  @override
  Future<Coords?> current() async {
    final prefs = await SharedPreferences.getInstance();
    final lng = prefs.getDouble(_keyLng);
    final lat = prefs.getDouble(_keyLat);
    if (lng == null || lat == null) return fallback;
    final coords = Coords(lng: lng, lat: lat);
    return coords.valid ? coords : fallback;
  }

  Future<void> save(Coords coords) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setDouble(_keyLng, coords.lng);
    await prefs.setDouble(_keyLat, coords.lat);
  }
}

/// 真机定位来源（待接入）。
///
/// 接入步骤（拿到定位插件后把下面注释换成实现即可，业务层不用改）：
///   1. `flutter pub add geolocator`；
///   2. AndroidManifest 加 ACCESS_FINE_LOCATION / ACCESS_COARSE_LOCATION，
///      Info.plist 加 NSLocationWhenInUseUsageDescription；
///   3. 检查 LocationPermission，被拒时返回 null 让上层提示去设置里授权。
///
/// 现在直接返回 null：宁可让页面提示"未获取到定位"，
/// 也不要伪造一个坐标——假坐标进 GEO 索引会让所有人的附近列表一起飘。
class DeviceLocationSource implements LocationSource {
  const DeviceLocationSource();

  @override
  bool get isDevice => true;

  @override
  Future<Coords?> current() async => null;
}
