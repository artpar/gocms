# CRUD APIs

| GET    | /api/{entityName}                                         | Query Params                          | Request Body                                                                                  | Description                                                                                           |
| ------ | --------------------------------------------------------- | ------------------------------------- | --------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| POST   | /api/{entityName}                                         | page[size]= page[number] query filter |                                                                                               | Find all rows, paginated with query and filters                                                       |
| PATCH  | /api/{entityName}/{id}                                    |                                       | {"attributes": {  } "type": "{entityType} }                                                   | Update row by reference id                                                                            |
| PUT    | /api/{entityName}/{id}                                    |                                       | {"attributes": { } "type": "{entityType} }                                                    | Update row by reference id                                                                            |
| DELETE | /api/{entityName}/{id}                                    |                                       |                                                                                               | Delete a row                                                                                          |


# Relation APIs

| GET    | /api/{entityName}/{id}/{relationName}                     | page[size]= page[number] query filter |                                                                                               | Find all related rows by relation name, eg, "posts" of a user                                         |
| DELETE | /api/{entityName}/{id}/{relationName}                     |                                       | {"id": , "type":  }                                                                           | Delete a related row, eg: delete post of a user. this only removes a relation and not the actual row. |
| GET    | /action/{entityName}/{actionName}                         | Parameters for action                 |                                                                                               | Invoke an action on an entity                                                                         |
| POST   | /action/{entityName}/{actionName}                         |                                       | { "attribute": { Parameters for action }, "id": "< object id >" type: "< entity type >" }     | Invoke an action on an entity                                                                         |


# State machine APIs


| POST   | /track/start/{stateMachineId}                             |                                       | { "id": " < reference id >", type: " < entity type > " }                                      | Start tracking according to the state machine for an object                                           |
| POST   | /track/event/{typename}/{objectStateId}/{eventName}       |                                       |                                                                                               | Invoke an event on a particular track of the state machine for a object                               |


# Websocket API

| GET    | /live                                                     |                                       |                                                                                               | Initiate a web socket connection                                                                      |


# Metadata API

| GET    | /apispec.raml                                             |                                       |                                                                                               | RAML Spec for all API's                                                                               |
| GET    | /ping                                                     |                                       |                                                                                               | Replies with PONG, Good for liveness probe                                                            |

