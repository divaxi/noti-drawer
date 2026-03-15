package notidrawer

type DaemonError struct {
	Phase   DaemonPhase `json:"-"`
	Err     error       `json:"-"`
	Message string      `json:"error"`
}

func (e DaemonError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

type DaemonPhase int

const (
	PhaseUnknown   DaemonPhase = iota // 0: Trạng thái không xác định
	PhaseStarting                     // 1: Đang khởi động
	PhaseIndexing                     // 2: Đang quét cấu hình (cái bạn đang làm)
	PhaseProvision                    // 3: Đang cấp phát tài nguyên
	PhaseRunning                      // 4: Đang chạy ổn định
	PhaseReloading                    // 5: Đang nạp lại cấu hình
	PhaseStopping                     // 6: Đang dừng daemon
)

func (p DaemonPhase) String() string {
	switch p {
	case PhaseUnknown:
		return "UNKNOWN"
	case PhaseStarting:
		return "STARTING"
	case PhaseIndexing:
		return "INDEXING"
	case PhaseProvision:
		return "PROVISION"
	case PhaseRunning:
		return "RUNNING"
	case PhaseReloading:
		return "RELOADING"
	case PhaseStopping:
		return "STOPPING"
	default:
		return "INVALID"
	}
}
