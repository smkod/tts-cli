package core

// ValidBitrate reports the constant bitrates supported by the MP3 encoder.
func ValidBitrate(n int) bool {
	for _, rate := range []int{8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 192, 224, 256, 320} {
		if n == rate {
			return true
		}
	}
	return false
}
