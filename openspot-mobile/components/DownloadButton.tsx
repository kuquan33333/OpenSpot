import React, { useState, useEffect, useRef } from 'react';
import {
  TouchableOpacity,
  Animated,
  Easing,
  StyleSheet,
  StyleProp,
  ViewStyle,
  Text
} from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import * as Haptics from 'expo-haptics';
import { Track } from '../types/music';
import { downloadTrack, getOfflineDownload } from '@/lib/offline-download-service';
import { useTranslation } from 'react-i18next';


const ANIMATION_DURATION = 350;
const ANIMATION_BOUNCE_HEIGHT = -10;
const ICON_SIZE = 24;

const downloadingTrackIds: Set<string> = new Set();

interface DownloadButtonProps {
  track: Track;
  style?: StyleProp<ViewStyle>;
  onDownloaded?: (filePath: string) => void;
  iconColor?: string;
  accentColor?: string;
  showNotification: (message: string, type: 'success' | 'error') => void;
  iconSize?: number;
  showText?: boolean;
  textColor?: string;
}

export const DownloadButton: React.FC<DownloadButtonProps> = ({
  track,
  style,
  onDownloaded,
  iconColor = '#fff',
  accentColor = '#1DB954',
  showNotification,
  iconSize,
  showText = false,
  textColor
}) => {
  const { t } = useTranslation();
  const [isDownloaded, setIsDownloaded] = useState(false);
  const [isDownloading, setIsDownloading] = useState(false);

  const bounceAnim = useRef(new Animated.Value(0)).current;
  useEffect(() => {
    let isMounted = true;

    const checkDownloaded = async () => {
      if (!track || !track.id) return;

      try {
        const metadata = await getOfflineDownload(String(track.id));
        if (isMounted) setIsDownloaded(Boolean(metadata));
      } catch (error) {
        console.error('Error checking download status:', error);
        if (isMounted) setIsDownloaded(false);
      }
    };

    checkDownloaded();

    return () => {
      isMounted = false;
    };
  }, [track]);

  useEffect(() => {
    let animation: Animated.CompositeAnimation | null = null;

    if (isDownloading) {
      animation = Animated.loop(
        Animated.sequence([
          Animated.timing(bounceAnim, {
            toValue: ANIMATION_BOUNCE_HEIGHT,
            duration: ANIMATION_DURATION,
            easing: Easing.inOut(Easing.ease),
            useNativeDriver: true,
          }),
          Animated.timing(bounceAnim, {
            toValue: 0,
            duration: ANIMATION_DURATION,
            easing: Easing.inOut(Easing.ease),
            useNativeDriver: true,
          }),
        ])
      );
      animation.start();
    } else {
      bounceAnim.stopAnimation();
      bounceAnim.setValue(0);
    }

    return () => {
      if (animation) {
        animation.stop();
      }
    };
  }, [isDownloading, bounceAnim]);

  useEffect(() => {
    if (track?.id && downloadingTrackIds.has(track.id.toString())) {
      setIsDownloading(true);
    }
  }, [track?.id]);

  const handleDownload = async () => {
    if (isDownloading || isDownloaded) return;
    if (!track || !track.id) {
      console.error("Cannot download: Track or track.id is undefined.");
      showNotification(t('components.download_error_track_missing') || 'Could not download track. Track data is missing.', 'error');
      return;
    }

    try {
      setIsDownloading(true);
      downloadingTrackIds.add(track.id.toString());
      Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
      const result = await downloadTrack(track);

      setIsDownloaded(true);
      showNotification(t('components.downloaded') || 'Downloaded', 'success'); 

      if (onDownloaded) {
        onDownloaded(result.fileUri);
      }

      Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);

    } catch (e: any) {
      console.error('Offline download failed:', e);

      const errorMessage = e instanceof Error ? e.message : 'Unknown error';
      showNotification(t('components.download_failed') || `Download failed: ${errorMessage}`, 'error'); // Call parent notification

      Haptics.notificationAsync(Haptics.NotificationFeedbackType.Error);

    } finally {
      setIsDownloading(false);
      if (track?.id) downloadingTrackIds.delete(track.id.toString());
    }
  };


  const renderButtonContent = () => {
    const size = iconSize || ICON_SIZE;
    if (isDownloaded) {
      return <Ionicons name="checkmark" size={size} color={iconColor} />;
    } else if (isDownloading) {
      return (
        <Animated.View style={{ transform: [{ translateY: bounceAnim }] }}>
          <Ionicons name="cloud-download-outline" size={size} color={accentColor} />
        </Animated.View>
      );
    } else {
      return <Ionicons name="download" size={size} color={iconColor} />;
    }
  };

  return (
    <TouchableOpacity
      onPress={handleDownload}
      style={[style, showText ? styles.downloadButtonWithText : styles.downloadButton]}
      activeOpacity={0.7}
      disabled={isDownloading}
    >
      {renderButtonContent()}
      {showText && (
        <Text style={[styles.downloadButtonText, { color: textColor || iconColor }]}>
          {isDownloaded ? (t('components.downloaded') || 'Downloaded') : (t('components.download') || 'Download')}
        </Text>
      )}
    </TouchableOpacity>
  );
};

const styles = StyleSheet.create({
  downloadButton: {
    padding: 4,
    justifyContent: 'center',
    alignItems: 'center',
  },
  downloadButtonWithText: {
    flexDirection: 'column',
    alignItems: 'center',
    justifyContent: 'center',
    padding: 8,
  },
  downloadButtonText: {
    fontSize: 10,
    marginTop: 4,
    fontWeight: '500',
  },
});
