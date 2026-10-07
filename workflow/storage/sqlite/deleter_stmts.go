package sqlite

const deletePlanByID = `DELETE FROM plans WHERE id = $id`

// deleteByPlanID deletes every row a plan has below it, one table each.
var deleteByPlanID = []string{
	`DELETE FROM blocks WHERE plan_id = $plan_id`,
	`DELETE FROM checks WHERE plan_id = $plan_id`,
	`DELETE FROM sequences WHERE plan_id = $plan_id`,
	`DELETE FROM actions WHERE plan_id = $plan_id`,
	`DELETE FROM deferredactions WHERE plan_id = $plan_id`,
	`DELETE FROM deferbatches WHERE plan_id = $plan_id`,
}
