$toolsDir = "$(Split-Path -Parent $MyInvocation.MyCommand.Definition)"
$url = 'https://github.com/tamld/g8s/releases/download/v0.23.0/g8s_0.23.0_windows_amd64.zip'
$checksum = '<from checksums.txt>'
$packageArgs = @{
  packageName    = 'g8s'
  unzipLocation  = $toolsDir
  url            = $url
  checksum       = $checksum
  checksumType   = 'sha256'
}
Install-ChocolateyZipPackage @packageArgs
