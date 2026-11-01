import 'package:flutter/material.dart';
import 'package:tencent_rtc_sdk/trtc_cloud_video_view.dart';

import 'data/rtc_bridge.dart';

/// TRTC 视频渲染区：远端全屏，本地预览悬浮在右上角。
class RtcVideoPanel extends StatelessWidget {
  const RtcVideoPanel({super.key, required this.bridge});

  final RtcBridge bridge;

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: bridge,
      builder: (context, _) {
        final remotes = bridge.remoteVideoUsers;
        return ClipRRect(
          borderRadius: BorderRadius.circular(20),
          child: ColoredBox(
            color: Colors.black,
            child: Stack(
              fit: StackFit.expand,
              children: [
                if (remotes.isNotEmpty)
                  TRTCCloudVideoView(
                    key: ValueKey('remote_${remotes.first}'),
                    onViewCreated: (viewId) =>
                        bridge.startRemoteView(remotes.first, viewId),
                  )
                else
                  Center(
                    child: Text(
                      bridge.lastError ?? '等待对方画面…',
                      style: const TextStyle(color: Colors.white70),
                    ),
                  ),
                Positioned(
                  right: 12,
                  top: 12,
                  width: 108,
                  height: 152,
                  child: ClipRRect(
                    borderRadius: BorderRadius.circular(12),
                    child: ColoredBox(
                      color: Colors.black54,
                      child: TRTCCloudVideoView(
                        key: const ValueKey('local_video'),
                        onViewCreated: bridge.startLocalPreview,
                      ),
                    ),
                  ),
                ),
              ],
            ),
          ),
        );
      },
    );
  }
}
