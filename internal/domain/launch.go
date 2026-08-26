package domain

import "encoding/json"

// LaunchBlocker is a specific reason an ad account cannot be launched into.
type LaunchBlocker string

const (
	BlockerNotActive     LaunchBlocker = "account_not_active"
	BlockerDisabled      LaunchBlocker = "account_disabled"
	BlockerNoFunding     LaunchBlocker = "no_funding_source"
	BlockerNoPermission  LaunchBlocker = "missing_advertise_permission"
	BlockerSpendCapMet   LaunchBlocker = "spend_cap_reached"
	BlockerNotDiscovered LaunchBlocker = "not_in_inventory"
)

// LaunchReadiness explains whether an account can be launched into, and if
// not, precisely why.
//
// The launcher shows blocked accounts rather than hiding them: "why is my
// account missing from the list" is a worse question to be left with than a
// row that says the card was removed.
type LaunchReadiness struct {
	Ready    bool            `json:"ready"`
	Blockers []LaunchBlocker `json:"blockers,omitempty"`
}

// AdvertiseTask is the permission Meta requires to create ads on an account.
// DRAFT alone allows building but not publishing.
const AdvertiseTask = "ADVERTISE"
const ManageTask = "MANAGE"

// LaunchReadinessFor evaluates one ad account.
func LaunchReadinessFor(account *AdAccount) LaunchReadiness {
	if account == nil {
		return LaunchReadiness{Blockers: []LaunchBlocker{BlockerNotDiscovered}}
	}
	var blockers []LaunchBlocker

	// 1 is ACTIVE. Everything else - disabled, closed, unsettled, in review -
	// either cannot serve or will reject the write.
	if account.AccountStatus != 1 {
		blockers = append(blockers, BlockerNotActive)
	}
	if account.DisableReason != 0 {
		blockers = append(blockers, BlockerDisabled)
	}
	// A missing funding instrument is the only dependable "no money" signal.
	// balance is the amount due, not the amount available, so a busy account
	// on a credit line reports zero while spending thousands.
	if account.FundingSource == "" {
		blockers = append(blockers, BlockerNoFunding)
	}
	if !account.CanAdvertise() {
		blockers = append(blockers, BlockerNoPermission)
	}
	// A spend cap already reached means Meta will accept the objects and then
	// refuse to deliver them, which looks like a silent failure.
	if account.SpendCap > 0 && account.AmountSpent >= account.SpendCap {
		blockers = append(blockers, BlockerSpendCapMet)
	}
	return LaunchReadiness{Ready: len(blockers) == 0, Blockers: blockers}
}

// CanAdvertise reports whether the connected user may publish on this account.
func (a *AdAccount) CanAdvertise() bool {
	var tasks []string
	if len(a.UserTasks) == 0 {
		return false
	}
	if err := json.Unmarshal(a.UserTasks, &tasks); err != nil {
		return false
	}
	for _, task := range tasks {
		if task == AdvertiseTask || task == ManageTask {
			return true
		}
	}
	return false
}

// RemainingSpendCap reports how much of a cap is left, or -1 when uncapped.
func (a *AdAccount) RemainingSpendCap() int64 {
	if a.SpendCap <= 0 {
		return -1
	}
	remaining := a.SpendCap - a.AmountSpent
	if remaining < 0 {
		return 0
	}
	return remaining
}
