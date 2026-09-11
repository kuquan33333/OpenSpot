Pod::Spec.new do |s|
  s.name = 'openspot-extension-core'
  s.module_name = 'openspot_extension_core'
  s.version = '1.0.0'
  s.summary = 'OpenSpot native SpotiFLAC Extension Core bridge'
  s.description = 'Loads the pinned Go Extension Core through a native Expo module.'
  s.homepage = 'https://github.com/todo996/openspot-music-app'
  s.license = { :type => 'MIT' }
  s.author = { 'OpenSpot' => 'todo996' }
  s.platforms = { :ios => '15.1' }
  s.source = { :path => '.' }
  s.source_files = 'ios/**/*.{swift,h,m,mm}'
  s.vendored_frameworks = 'ios/Frameworks/Gobackend.xcframework'
  s.swift_version = '5.9'
  s.pod_target_xcconfig = { 'DEFINES_MODULE' => 'YES' }
  s.dependency 'ExpoModulesCore'
end
