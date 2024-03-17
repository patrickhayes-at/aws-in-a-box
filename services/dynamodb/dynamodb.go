package dynamodb

import (
	"log/slog"
	"reflect"
	"sync"

	"aws-in-a-box/arn"
	"aws-in-a-box/awserrors"
)

type Table struct {
	Name                 string
	ARN                  string
	BillingMode          string
	AttributeDefinitions []APIAttributeDefinition
	KeySchema            []APIKeySchemaElement

	PrimaryKeyAttributeName string
	ItemByPrimaryKey        map[string]APIItem
}

func (t *Table) toAPI() APITableDescription {
	return APITableDescription{
		AttributeDefinitions: t.AttributeDefinitions,
		ItemCount:            len(t.ItemByPrimaryKey),
		KeySchema:            t.KeySchema,
		// TODO: delayed creation
		TableARN:    t.ARN,
		TableStatus: "ACTIVE",
	}
}

type DynamoDB struct {
	logger       *slog.Logger
	arnGenerator arn.Generator

	mu           sync.Mutex
	tablesByName map[string]*Table
}

func New(logger *slog.Logger, generator arn.Generator) *DynamoDB {
	if logger == nil {
		logger = slog.Default()
	}

	d := &DynamoDB{
		logger:       logger,
		arnGenerator: generator,
		tablesByName: make(map[string]*Table),
	}
	return d
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_CreateTable.html
func (d *DynamoDB) CreateTable(input CreateTableInput) (*CreateTableOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.tablesByName[input.TableName]; ok {
		return nil, awserrors.ResourceInUseException("Table already exists")
	}

	primaryKeyAttributeName := ""
	for _, keySchemaElement := range input.KeySchema {
		if keySchemaElement.KeyType == "HASH" {
			primaryKeyAttributeName = keySchemaElement.AttributeName
			break
		}
	}
	if primaryKeyAttributeName == "" {
		return nil, awserrors.InvalidArgumentException("KeySchema must have a HASH key")
	}

	t := &Table{
		Name:                    input.TableName,
		ARN:                     d.arnGenerator.Generate("dynamodb", "table", input.TableName),
		BillingMode:             input.BillingMode,
		AttributeDefinitions:    input.AttributeDefinitions,
		KeySchema:               input.KeySchema,
		PrimaryKeyAttributeName: primaryKeyAttributeName,
		ItemByPrimaryKey:        make(map[string]APIItem),
	}
	d.tablesByName[input.TableName] = t

	return &CreateTableOutput{
		TableDescription: t.toAPI(),
	}, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_CreateTable.html
func (d *DynamoDB) DescribeTable(input DescribeTableInput) (*DescribeTableOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.tablesByName[input.TableName]
	if !ok {
		return nil, awserrors.ResourceNotFoundException("Table does not exist")
	}

	return &DescribeTableOutput{
		Table: t.toAPI(),
	}, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Scan.html
func (d *DynamoDB) Scan(input ScanInput) (*ScanOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.tablesByName[input.TableName]
	if !ok {
		return nil, awserrors.ResourceNotFoundException("Table does not exist")
	}

	var allItems []APIItem
	for _, item := range t.ItemByPrimaryKey {
		// TODO: filter
		allItems = append(allItems, item)
	}

	return &ScanOutput{
		Count: len(allItems),
		Items: allItems,
	}, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_PutItem.html
func (d *DynamoDB) PutItem(input PutItemInput) (*PutItemOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.tablesByName[input.TableName]
	if !ok {
		return nil, awserrors.ResourceNotFoundException("Table does not exist")
	}
	// TODO: Number and Binary primary key
	key := input.Item[t.PrimaryKeyAttributeName].S
	if key == "" {
		return nil, awserrors.InvalidArgumentException("PrimaryKey must be provided (and string)")
	}
	t.ItemByPrimaryKey[key] = input.Item

	return &PutItemOutput{}, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_GetItem.html
func (d *DynamoDB) GetItem(input GetItemInput) (*GetItemOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.tablesByName[input.TableName]
	if !ok {
		return nil, awserrors.ResourceNotFoundException("Table does not exist")
	}

	// TODO: composite keys
	// TODO: Binary and Number primary keys
	key := input.Key[t.PrimaryKeyAttributeName].S
	if key == "" {
		return nil, awserrors.InvalidArgumentException("PrimaryKey must be provided (and string)")
	}
	item := t.ItemByPrimaryKey[key]

	return &GetItemOutput{Item: item}, nil
}

// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateItem.html
func (d *DynamoDB) UpdateItem(input UpdateItemInput) (*UpdateItemOutput, *awserrors.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	t, ok := d.tablesByName[input.TableName]
	if !ok {
		return nil, awserrors.ResourceNotFoundException("Table does not exist")
	}

	// TODO: composite keys
	key := input.Key[t.PrimaryKeyAttributeName].S
	if key == "" {
		return nil, awserrors.InvalidArgumentException("PrimaryKey must be provided (and string)")
	}
	existingItem, ok := t.ItemByPrimaryKey[key]

	if !ok {
		existingItem = make(map[string]APIAttributeValue)
	}

	// Check preconditions
	for attribute, expectation := range input.Expected {
		attr, exists := existingItem[attribute]
		if expectation.Exists != nil {
			if *expectation.Exists != exists {
				return nil, awserrors.XXX_TODO("Attribute exists mismatch")
			}
		}
		switch expectation.ComparisonOperator {
		case "":
		case "EQ":
			if !reflect.DeepEqual(attr, expectation.Value) {
				return nil, awserrors.XXX_TODO("Attribute EQ mismatch")
			}
		case "NEQ":
			if reflect.DeepEqual(attr, expectation.Value) {
				return nil, awserrors.XXX_TODO("Attribute NEQ mismatch")
			}
		default:
			return nil, awserrors.InvalidArgumentException("Invalid expectation comparison operator: " + expectation.ComparisonOperator)
		}
	}

	// Perform update
	// TODO: handle ReturnValues
	for attribute, update := range input.AttributeUpdates {
		switch update.Action {
		case "PUT":
			existingItem[attribute] = update.Value
		case "DELETE":
			delete(existingItem, attribute)
		case "ADD":
			// TODO
			// fallthrough
		default:
			return nil, awserrors.InvalidArgumentException("Invalid update action: " + update.Action)
		}
	}

	// If this was an insert, not an update, we need to commit it.
	if !ok {
		t.ItemByPrimaryKey[key] = existingItem
	}

	return &UpdateItemOutput{}, nil
}
