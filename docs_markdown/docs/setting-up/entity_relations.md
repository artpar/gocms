# Data relations

A data oriented system with no relational knowledge of the data is next to an Excel sheet. Specifying relations in your data is the most important thing after creating your entities.

Relations are constraints among tables and help you keep clean and consistent data. Relational data is easily accessible over APIs using a path structure like `/api/<entityName>/<id>/<relationName>` and the response is consistent with [JSONAPI.org](https://JSONAPI.org).

Checkout the [relation apis](/apis/relation.md) exposed by daptin.

!!! note "YAML example"
    ```yaml
    Relations:
    - Subject: todo
      Relation: has_one
      Object: project
    ```

!!! note "JSON example"
    ```json
    {
      "Relations": [
        {
          "Subject": "todo",
          "Relation": "has_one",
          "Object": "project"
        }
      ]
    }
    ```


## Relations in JSON/YAML schema

When uploading schema using a JSON / YAML file, relations can be added in the same file and daptin will create appropriate constraints and foreign keys in your underlying database.

Continuing with our example of todos, lets say we want to group todo's in "projects" and each todo can belong to only a single project.

Lets design a "project" entity:

```yaml
- TableName: project
  Columns:
  - Name: name
    DataType: varchar(200)
    ColumnType: name
    IsIndexed: true
```

A very simple table with just a name column. Now we can tell daptin about the relation between todos and projects

```yaml
Relations:
- Subject: todo
  Relation: has_one
  Object: project
```

This tells daptin that todo "has_one" project.

## Relations types

Any entity can be associated to any other entity (or to itself) as one of the follows

Relation Name | Relation Descriptio | Can be empty 
--- | --- | ---
belongs_to | a single object relation | No
has_one | a single object relation | Yes
has_many | many related objects | Yes

## Default relations

Every entity created on daptin has at least two relations

Relation Type | Related Entity | Purpose
--- | --- | ---
belongs | user | owner of the object
has many | usergroup | belongs to usergroup


These relations help you precisely control the authorization for each user.

Read more about [authorization and permissions](/auth/authorization.md)


## Multiple relation

There can be a scenario where two entities are related in more then 1 way. Consider the following example

- A blog entity
- A post entity
- Blog has many posts
- Each blog can have a "highlighted post" (blog has one "highlighted post")

To achieve the above scenario, our schema would look like as follows

```yaml
Tables:
- TableName: blog
  Columns:
  - Name: title
    DataType: varchar(500)
    ColumnType: label
  - Name: view_count
    DataType: int(11)
    ColumnType: measurement
- TableName: post
  Columns:
  - Name: title
    DataType: varchar(200)
    ColumnType: label
  - Name: body
    DataType: text
    ColumnType: content
- TableName: comment
  Columns:
  - Name: body
    DataType: text
    ColumnType: content
  - Name: likes_count
    ColumnName: likes_count
    DataType: int(11)
    ColumnType: measurement
Relations:
- Subject: comment
  Relation: belongs_to
  Object: post
- Subject: post
  Relation: belongs_to
  Object: blog                   // this is our post belongs to blog relation
- Subject: blog
  Relation: has_one
  Object: post
  ObjectName: current_post
  SubjectName: current_post_of   // this is our highlighted post relation
```

Notice the "SubjectName" and "ObjectName" keys which helps to name our relations more intuitively.


## SQL constraints

### belongs to

- A column is added to the subject entity, which refers to the Object entity, set to non nullable

### has one

- Same as above, but nullable

### has many

- A join table is created