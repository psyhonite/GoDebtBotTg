package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
)

// --- Data Structures ---

type Debt struct {
	ID       int
	DebtorID int
	Amount   float64
	Reason   string
}

type Debtor struct {
	ID     int
	Name   string
	ChatID int64
}

// --- Global Variables ---

var DB *sql.DB

// Conversation states (simplified and made more descriptive)
const (
	StateIdle                    = iota // Bot is waiting for a command or a new interaction
	StateAddingDebtorName               // Waiting for the user to enter a debtor's name
	StateAddingDebtReason               // Waiting for the reason for the debt
	StateAddingDebtAmount               // Waiting for the amount of the debt
	StateEditingChooseDebt              // User is choosing a debt to edit/close from an inline keyboard
	StateEditingChooseWhatToEdit        // User is choosing whether to edit amount or reason
	StateEditingAmount                  // User is entering a new amount
	StateEditingReason                  // User is entering a new reason
	StateConfirmingCloseDebt            //User is confirming that they want to close a debt
)

var userStates = make(map[int64]int)        // Tracks the current state of each user's interaction
var currentDebtors = make(map[int64]Debtor) // Stores the currently selected debtor for each user
var selectedDebts = make(map[int64]Debt)    // Stores the currently selected debt (for editing/closing)

// --- Helper Functions ---

// sendWithKeyboard sends a message with an optional inline keyboard.  Makes code cleaner.
func sendWithKeyboard(bot *tgbotapi.BotAPI, chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown" // Use Markdown for formatting
	if keyboard.InlineKeyboard != nil {
		msg.ReplyMarkup = keyboard
	}
	_, err := bot.Send(msg)
	if err != nil {
		log.Printf("Error sending message: %v", err)
	}
}

// sendSimpleMessage sends a plain text message (no keyboard).
func sendSimpleMessage(bot *tgbotapi.BotAPI, chatID int64, text string) {
	sendWithKeyboard(bot, chatID, text, tgbotapi.InlineKeyboardMarkup{})
}

// editMessageWithKeyboard edits an existing message and optionally updates the inline keyboard.
func editMessageWithKeyboard(bot *tgbotapi.BotAPI, chatID int64, messageID int, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ParseMode = "Markdown"
	if keyboard.InlineKeyboard != nil {
		editMsg.ReplyMarkup = &keyboard
	}
	_, err := bot.Send(editMsg)
	if err != nil {
		log.Printf("Error editing message: %v", err)
	}
}

// clearUserState resets the user's state and clears related data.  Important for preventing stale data.
func clearUserState(chatID int64) {
	delete(userStates, chatID)
	delete(currentDebtors, chatID)
	delete(selectedDebts, chatID)
}

// --- Database Initialization ---

func initDB() {
	var err error
	DB, err = sql.Open("sqlite3", "./debt_tracker.db")
	if err != nil {
		log.Fatal(err)
	}

	createDebtorsTable := `
        CREATE TABLE IF NOT EXISTS debtors (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            chat_id INTEGER NOT NULL,
            UNIQUE(name, chat_id)  -- Ensure unique debtor names per chat
        );`
	_, err = DB.Exec(createDebtorsTable)
	if err != nil {
		log.Fatal(err)
	}

	createDebtsTable := `
        CREATE TABLE IF NOT EXISTS debts (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            debtor_id INTEGER NOT NULL,
            amount REAL NOT NULL,
            reason TEXT NOT NULL,
            FOREIGN KEY (debtor_id) REFERENCES debtors (id) ON DELETE CASCADE -- Cascade deletion
        );`
	_, err = DB.Exec(createDebtsTable)
	if err != nil {
		log.Fatal(err)
	}
}

// --- Database Interaction Functions ---

func addDebtor(debtor Debtor) (Debtor, error) {
	result, err := DB.Exec("INSERT INTO debtors (name, chat_id) VALUES (?, ?)", debtor.Name, debtor.ChatID)
	if err != nil {
		// Check for unique constraint violation (debtor already exists for this chat)
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return debtor, fmt.Errorf("debtor already exists")
		}
		return debtor, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return debtor, err
	}
	debtor.ID = int(id)
	return debtor, nil
}

func getDebtorByName(name string, chatID int64) (Debtor, error) {
	var debtor Debtor
	err := DB.QueryRow("SELECT id, name, chat_id FROM debtors WHERE name = ? AND chat_id = ?", name, chatID).Scan(&debtor.ID, &debtor.Name, &debtor.ChatID)
	return debtor, err
}

func getDebtorByID(id int) (Debtor, error) {
	var debtor Debtor
	err := DB.QueryRow("SELECT id, name, chat_id FROM debtors WHERE id = ?", id).Scan(&debtor.ID, &debtor.Name, &debtor.ChatID)
	return debtor, err
}

func addDebt(debt Debt) error {
	_, err := DB.Exec("INSERT INTO debts (debtor_id, amount, reason) VALUES (?, ?, ?)", debt.DebtorID, debt.Amount, debt.Reason)
	return err
}

func listDebtors(chatID int64) ([]Debtor, error) {
	rows, err := DB.Query("SELECT id, name FROM debtors WHERE chat_id = ?", chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var debtors []Debtor
	for rows.Next() {
		var debtor Debtor
		if err := rows.Scan(&debtor.ID, &debtor.Name); err != nil {
			return nil, err
		}
		debtors = append(debtors, debtor)
	}
	return debtors, rows.Err()
}

func listDebts(debtorID int) ([]Debt, error) {
	rows, err := DB.Query("SELECT id, amount, reason FROM debts WHERE debtor_id = ?", debtorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var debts []Debt
	for rows.Next() {
		var debt Debt
		if err := rows.Scan(&debt.ID, &debt.Amount, &debt.Reason); err != nil {
			return nil, err
		}
		debts = append(debts, debt)
	}
	return debts, rows.Err()
}

func getDebtByID(debtID int) (Debt, error) {
	var debt Debt
	err := DB.QueryRow("SELECT id, debtor_id, amount, reason FROM debts WHERE id = ?", debtID).Scan(&debt.ID, &debt.DebtorID, &debt.Amount, &debt.Reason)
	return debt, err
}

func updateDebtAmount(debtID int, newAmount float64) error {
	_, err := DB.Exec("UPDATE debts SET amount = ? WHERE id = ?", newAmount, debtID)
	return err
}

func updateDebtReason(debtID int, newReason string) error {
	_, err := DB.Exec("UPDATE debts SET reason = ? WHERE id = ?", newReason, debtID)
	return err
}
func closeDebt(debtID int) error {
	_, err := DB.Exec("DELETE FROM debts WHERE id = ?", debtID)
	return err
}

// --- Command Handlers ---

func handleStartCommand(bot *tgbotapi.BotAPI, chatID int64) {
	clearUserState(chatID) // Always start with a clean slate
	text := "Привет! Я бот DebtTracker. Я помогу тебе вести учет долгов.\n\n" +
		"**Основные команды:**\n" +
		"/add - Добавить долг\n" +
		"/debts - Посмотреть список должников и долги\n" +
		"/help - Помощь и список команд"
	sendSimpleMessage(bot, chatID, text)
}

func handleAddCommand(bot *tgbotapi.BotAPI, chatID int64) {
	clearUserState(chatID) // Clear any previous state
	userStates[chatID] = StateAddingDebtorName
	sendSimpleMessage(bot, chatID, "Введи имя должника:")
}

func handleDebtsCommand(bot *tgbotapi.BotAPI, chatID int64) {
	clearUserState(chatID)

	debtors, err := listDebtors(chatID)
	if err != nil {
		log.Printf("Error listing debtors: %v", err)
		sendSimpleMessage(bot, chatID, "Произошла ошибка при получении списка должников.")
		return
	}

	if len(debtors) == 0 {
		sendSimpleMessage(bot, chatID, "У тебя пока нет должников.  Используй /add, чтобы добавить.")
		return
	}

	var keyboardButtons [][]tgbotapi.InlineKeyboardButton
	for _, debtor := range debtors {
		debts, _ := listDebts(debtor.ID) // Get debts to show count (ignore error for brevity here)
		debtPlural := "долга"
		if len(debts)%10 == 1 && len(debts)%100 != 11 {
			debtPlural = "долг"
		} else if (len(debts)%10 >= 2 && len(debts)%10 <= 4) && !(len(debts)%100 >= 12 && len(debts)%100 <= 14) {
			debtPlural = "долга"
		} else {
			debtPlural = "долгов"
		}

		buttonText := fmt.Sprintf("%s (%d %s)", debtor.Name, len(debts), debtPlural)
		callbackData := fmt.Sprintf("select_debtor:%d", debtor.ID)
		keyboardButtons = append(keyboardButtons, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(buttonText, callbackData)))
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(keyboardButtons...)
	sendWithKeyboard(bot, chatID, "*Твои должники:*", keyboard)
}

func handleHelpCommand(bot *tgbotapi.BotAPI, chatID int64) {
	clearUserState(chatID)
	text := "**Команды бота DebtTracker:**\n\n" +
		"/add - Добавить новый долг.  Бот спросит имя должника, причину и сумму.\n" +
		"/debts - Показать список всех твоих должников.  Можно выбрать должника, чтобы увидеть детализацию долгов, закрыть или отредактировать долги.\n" +
		"/help - Показать это сообщение со списком команд."
	sendSimpleMessage(bot, chatID, text)
}

// --- Message Handler ---

func handleMessage(bot *tgbotapi.BotAPI, update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	text := update.Message.Text
	state := userStates[chatID]

	switch state {
	case StateAddingDebtorName:
		// Try to find existing debtor (for this chat)
		debtor, err := getDebtorByName(text, chatID)
		if err != nil && err != sql.ErrNoRows {
			log.Printf("Error getting debtor: %v", err)
			sendSimpleMessage(bot, chatID, "Произошла ошибка при поиске должника.")
			clearUserState(chatID)
			return
		}

		if err == sql.ErrNoRows {
			// Debtor doesn't exist, create a new one
			newDebtor := Debtor{Name: text, ChatID: chatID}
			newDebtor, err = addDebtor(newDebtor) //addDebtor now return debtor struct
			if err != nil {
				if strings.Contains(err.Error(), "debtor already exists") {
					sendSimpleMessage(bot, chatID, fmt.Sprintf("Должник с именем *%s* уже существует в вашем списке. Пожалуйста введите другое имя", text))
					return
				}
				log.Printf("Error adding debtor: %v", err)
				sendSimpleMessage(bot, chatID, "Произошла ошибка при добавлении должника.")
				clearUserState(chatID)
				return
			}
			currentDebtors[chatID] = newDebtor // Store the new debtor
		} else {
			// Debtor exists
			currentDebtors[chatID] = debtor
		}

		userStates[chatID] = StateAddingDebtReason
		sendSimpleMessage(bot, chatID, fmt.Sprintf("Какова причина долга для *%s*?", currentDebtors[chatID].Name))

	case StateAddingDebtReason:
		selectedDebts[chatID] = Debt{DebtorID: currentDebtors[chatID].ID, Reason: text}
		userStates[chatID] = StateAddingDebtAmount
		sendSimpleMessage(bot, chatID, fmt.Sprintf("Сколько *%s* должен за *%s*?", currentDebtors[chatID].Name, text))

	case StateAddingDebtAmount:
		amount, err := strconv.ParseFloat(text, 64)
		if err != nil || amount <= 0 { // Validate amount
			sendSimpleMessage(bot, chatID, "Пожалуйста, введи корректную сумму долга (положительное число).")
			return // Don't change state, ask for amount again
		}

		debt := Debt{DebtorID: currentDebtors[chatID].ID, Amount: amount, Reason: selectedDebts[chatID].Reason}
		if err := addDebt(debt); err != nil {
			log.Printf("Error adding debt: %v", err)
			sendSimpleMessage(bot, chatID, "Произошла ошибка при добавлении долга.")
		} else {
			sendSimpleMessage(bot, chatID, fmt.Sprintf("✅ Долг добавлен! *%s* должен *%.2f ₽* за *%s*.", currentDebtors[chatID].Name, amount, debt.Reason))
		}
		clearUserState(chatID) // Clear state after adding the debt

	case StateEditingAmount:
		amount, err := strconv.ParseFloat(text, 64)
		if err != nil || amount <= 0 {
			sendSimpleMessage(bot, chatID, "Пожалуйста, введи корректную сумму (положительное число).")
			return
		}
		if err := updateDebtAmount(selectedDebts[chatID].ID, amount); err != nil {
			log.Printf("Error updating debt amount: %v", err)
			sendSimpleMessage(bot, chatID, "Не удалось обновить сумму долга.")
		} else {
			sendSimpleMessage(bot, chatID, "Сумма долга успешно обновлена.")
			showDebtorDetails(bot, chatID, currentDebtors[chatID].ID) // Refresh details
		}

		clearUserState(chatID)

	case StateEditingReason:
		if err := updateDebtReason(selectedDebts[chatID].ID, text); err != nil {
			log.Printf("Error updating debt reason: %v", err)
			sendSimpleMessage(bot, chatID, "Не удалось обновить причину долга.")
		} else {
			sendSimpleMessage(bot, chatID, "Причина долга успешно обновлена.")
			showDebtorDetails(bot, chatID, currentDebtors[chatID].ID)
		}

		clearUserState(chatID)

	default:
		// If the user sends a message in an unexpected state, guide them
		sendSimpleMessage(bot, chatID, "Чтобы добавить долг, используй команду /add.  Чтобы посмотреть долги, используй /debts.")
		clearUserState(chatID)
	}
}

// --- Callback Query Handler ---

func handleCallbackQuery(bot *tgbotapi.BotAPI, update tgbotapi.Update) {
	chatID := update.CallbackQuery.Message.Chat.ID
	messageID := update.CallbackQuery.Message.MessageID
	data := update.CallbackQuery.Data

	switch {
	case strings.HasPrefix(data, "select_debtor:"):
		debtorIDStr := strings.TrimPrefix(data, "select_debtor:")
		debtorID, err := strconv.Atoi(debtorIDStr)
		if err != nil {
			log.Printf("Invalid debtor ID in callback: %v", err)
			return
		}
		showDebtorDetails(bot, chatID, debtorID)

	case strings.HasPrefix(data, "close_debt:"):
		debtIDStr := strings.TrimPrefix(data, "close_debt:")
		debtID, err := strconv.Atoi(debtIDStr)
		if err != nil {
			log.Printf("Invalid debt ID in callback: %v", err)
			return
		}
		debt, err := getDebtByID(debtID)
		if err != nil {
			log.Printf("Error getting debt for closing: %v", err)
			return
		}
		selectedDebts[chatID] = debt // Store the selected debt
		userStates[chatID] = StateConfirmingCloseDebt
		//Confirmation Before Deleting
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ Да, закрыть", fmt.Sprintf("confirm_close:%d", debtID)),
				tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "cancel_close"),
			),
		)

		editMessageWithKeyboard(bot, chatID, messageID, fmt.Sprintf("Вы уверены, что хотите закрыть долг *%.2f ₽* за *%s*?", debt.Amount, debt.Reason), keyboard)

	case strings.HasPrefix(data, "confirm_close:"):
		debtIDStr := strings.TrimPrefix(data, "confirm_close:")
		debtID, _ := strconv.Atoi(debtIDStr) // Error handling omitted for brevity
		if err := closeDebt(debtID); err != nil {
			log.Printf("Error closing debt in callback: %v", err)
			sendSimpleMessage(bot, chatID, "Произошла ошибка при закрытии долга.")
			showDebtorDetails(bot, chatID, currentDebtors[chatID].ID)

		} else {
			editMessageWithKeyboard(bot, chatID, messageID, "Долг закрыт.", tgbotapi.InlineKeyboardMarkup{}) // Remove keyboard
			showDebtorDetails(bot, chatID, currentDebtors[chatID].ID)

		}
		clearUserState(chatID)

	case data == "cancel_close":
		editMessageWithKeyboard(bot, chatID, messageID, "Операция отменена.", tgbotapi.InlineKeyboardMarkup{})
		clearUserState(chatID)
		showDebtorDetails(bot, chatID, currentDebtors[chatID].ID)

	case strings.HasPrefix(data, "edit_debt:"):
		debtIDStr := strings.TrimPrefix(data, "edit_debt:")
		debtID, err := strconv.Atoi(debtIDStr)
		if err != nil {
			log.Printf("Invalid debt ID in callback: %v", err)
			return
		}
		debt, err := getDebtByID(debtID)
		if err != nil {
			log.Printf("Error getting debt for editing: %v", err)
			return
		}
		selectedDebts[chatID] = debt // Store the selected debt
		userStates[chatID] = StateEditingChooseWhatToEdit

		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Изменить сумму", fmt.Sprintf("edit_amount:%d", debtID)),
				tgbotapi.NewInlineKeyboardButtonData("Изменить причину", fmt.Sprintf("edit_reason:%d", debtID)),
			),
		)
		editMessageWithKeyboard(bot, chatID, messageID, "Что ты хочешь изменить?", keyboard)

	case strings.HasPrefix(data, "edit_amount:"):
		debtIDStr := strings.TrimPrefix(data, "edit_amount:")
		debtID, _ := strconv.Atoi(debtIDStr)
		selectedDebts[chatID] = Debt{ID: debtID} //Store only ID
		userStates[chatID] = StateEditingAmount
		editMessageWithKeyboard(bot, chatID, messageID, "Введи новую сумму:", tgbotapi.InlineKeyboardMarkup{})

	case strings.HasPrefix(data, "edit_reason:"):
		debtIDStr := strings.TrimPrefix(data, "edit_reason:")
		debtID, _ := strconv.Atoi(debtIDStr)
		selectedDebts[chatID] = Debt{ID: debtID}
		userStates[chatID] = StateEditingReason
		editMessageWithKeyboard(bot, chatID, messageID, "Введи новую причину:", tgbotapi.InlineKeyboardMarkup{})
	case data == "add_debt_to_existing":
		userStates[chatID] = StateAddingDebtReason
		editMessageWithKeyboard(bot, chatID, messageID, fmt.Sprintf("Какова причина долга для *%s*?", currentDebtors[chatID].Name), tgbotapi.InlineKeyboardMarkup{})

	}
}

// --- Show Debtor Details ---
func showDebtorDetails(bot *tgbotapi.BotAPI, chatID int64, debtorID int) {
	debtor, err := getDebtorByID(debtorID)
	if err != nil {
		log.Printf("Error getting debtor details: %v", err)
		sendSimpleMessage(bot, chatID, "Произошла ошибка при получении информации о должнике.")
		return
	}
	currentDebtors[chatID] = debtor

	debts, err := listDebts(debtorID)
	if err != nil {
		log.Printf("Error listing debts: %v", err)
		sendSimpleMessage(bot, chatID, "Произошла ошибка при получении списка долгов.")
		return
	}

	var totalDebt float64
	var debtsText strings.Builder
	debtsText.WriteString(fmt.Sprintf("*Долги %s:*\n\n", debtor.Name))
	var keyboardButtons [][]tgbotapi.InlineKeyboardButton

	for _, debt := range debts {
		debtsText.WriteString(fmt.Sprintf("- *%.2f ₽* за *%s*\n", debt.Amount, debt.Reason))
		totalDebt += debt.Amount

		// Buttons for editing and closing each debt
		keyboardButtons = append(keyboardButtons, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✏️ Редактировать", fmt.Sprintf("edit_debt:%d", debt.ID)),
			tgbotapi.NewInlineKeyboardButtonData("✅ Закрыть", fmt.Sprintf("close_debt:%d", debt.ID)),
		))
	}

	debtsText.WriteString(fmt.Sprintf("\n*Общая сумма долга: %.2f ₽*", totalDebt))
	keyboardButtons = append(keyboardButtons, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("➕ Добавить долг", "add_debt_to_existing"),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(keyboardButtons...)
	sendWithKeyboard(bot, chatID, debtsText.String(), keyboard)
}

// --- Main Function ---

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	bot, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_API_TOKEN"))
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = false // Set to true for debugging

	log.Printf("Authorized on account %s", bot.Self.UserName)

	initDB()
	defer DB.Close()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			if update.Message.IsCommand() {
				switch update.Message.Command() {
				case "start":
					handleStartCommand(bot, update.Message.Chat.ID)
				case "add":
					handleAddCommand(bot, update.Message.Chat.ID)
				case "debts":
					handleDebtsCommand(bot, update.Message.Chat.ID)
				case "help":
					handleHelpCommand(bot, update.Message.Chat.ID)
				default:
					sendSimpleMessage(bot, update.Message.Chat.ID, "Неизвестная команда. Используй /help для списка команд.")
					clearUserState(update.Message.Chat.ID)
				}
			} else {
				handleMessage(bot, update)
			}
		} else if update.CallbackQuery != nil {
			handleCallbackQuery(bot, update)
		}
	}
}
