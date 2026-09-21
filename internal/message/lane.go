package message

import (
	"hash/fnv"
	"strconv"

	"github.com/google/uuid"
)

// ExpressLane is the queue lane of every Express message.
const ExpressLane = "express"

// ExpressChannel is the notification channel signalled when Express messages
// are enqueued, so idle Express workers can claim them without polling.
const ExpressChannel = "express_ready"

// NormalLane returns the name of normal lane i.
func NormalLane(i int) string {
	return "normal-" + strconv.Itoa(i)
}

// Lane returns the queue lane for a message. All normal messages of one
// customer share a lane chosen by hashing the customer ID, which confines a
// customer's backlog to that lane.
func Lane(t Type, customerID uuid.UUID, normalLanes int) string {
	if t == Express {
		return ExpressLane
	}
	h := fnv.New32a()
	h.Write(customerID[:])
	return NormalLane(int(h.Sum32() % uint32(normalLanes)))
}
