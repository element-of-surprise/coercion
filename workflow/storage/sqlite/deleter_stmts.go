package sqlite

const deletePlanByID = `DELETE FROM plans WHERE id = $id`
const delteBlocksByID = `DELETE FROM blocks WHERE id = $id`
const deleteChecksByID = `DELETE FROM checks WHERE id = $id`
const deleteSequencesByID = `DELETE FROM sequences WHERE id = $id`
const deleteActionsByID = `DELETE FROM actions WHERE id = $id`
