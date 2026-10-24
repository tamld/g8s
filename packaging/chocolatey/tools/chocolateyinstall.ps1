$url = 'https://github.com/tamld/g8s/releases/download/v0.19.1/g8s_0.19.1_windows_amd64.msi'
$checksum = '<from checksums.txt>'
$packageArgs = @{
  packageName    = 'g8s'
  fileType       = 'MSI'
  url            = $url
  checksum       = $checksum
  checksumType   = 'sha256'
  silentArgs     = '/qn'
}
Install-ChocolateyPackage @packageArgs
