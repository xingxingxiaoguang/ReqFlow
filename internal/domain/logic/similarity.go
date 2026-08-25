package logic

// DistanceToScore 将余弦距离（0-2，越小越相似）换算为相似度分数（0-1，越大越相似）。
// 与向量库实现解耦：pgvector cosine distance 的值域与 PingCraft 时代 SeekDB 一致。
func DistanceToScore(distance float64) float64 {
	if distance < 0 {
		distance = 0
	}
	score := 1 - distance/2
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}
