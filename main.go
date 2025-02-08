package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

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
	ID            int
	Name          string
	ChatID        int64
	PaymentDate   sql.NullTime
	PaymentAmount sql.NullFloat64
}

// --- Global Variables ---

var DB *sql.DB

// Conversation states
const (
	StateIdle = iota
	StateAddingDebtorName
	StateAddingDebtReason
	StateAddingDebtAmount
	StateEditingChooseDebt
	StateEditingChooseWhatToEdit
	StateEditingAmount
	StateEditingReason
	StateConfirmingCloseDebt
	StateSubtractingFromDebt
	StateConfirmingDeleteDebtor
	StateSettingPaymentDate
	StateSettingPaymentAmount
	StateEditingPaymentDate
	StateEditingPaymentAmount
)

var userStates = make(map[int64]int)
var currentDebtors = make(map[int64]Debtor)
var selectedDebts = make(map[int64]Debt)

// --- Helper Functions ---

func sendWithKeyboard(bot *tgbotapi.BotAPI, chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if keyboard.InlineKeyboard != nil {
		msg.ReplyMarkup = keyboard
	}
	_, err := bot.Send(msg)
	if err != nil {
		log.Printf("Error sending message: %v", err)
	}
}

func sendSimpleMessage(bot *tgbotapi.BotAPI, chatID int64, text string) {
	sendWithKeyboard(bot, chatID, text, tgbotapi.InlineKeyboardMarkup{})
}

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
            payment_date DATETIME,
            payment_amount REAL,
            UNIQUE(name, chat_id)
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
            FOREIGN KEY (debtor_id) REFERENCES debtors (id) ON DELETE CASCADE
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
	err := DB.QueryRow("SELECT id, name, chat_id, payment_date, payment_amount FROM debtors WHERE name = ? AND chat_id = ?", name, chatID).Scan(&debtor.ID, &debtor.Name, &debtor.ChatID, &debtor.PaymentDate, &debtor.PaymentAmount)
	return debtor, err
}

func getDebtorByID(id int) (Debtor, error) {
	var debtor Debtor
	err := DB.QueryRow("SELECT id, name, chat_id, payment_date, payment_amount FROM debtors WHERE id = ?", id).Scan(&debtor.ID, &debtor.Name, &debtor.ChatID, &debtor.PaymentDate, &debtor.PaymentAmount)
	return debtor, err
}

func addDebt(debt Debt) error {
	_, err := DB.Exec("INSERT INTO debts (debtor_id, amount, reason) VALUES (?, ?, ?)", debt.DebtorID, debt.Amount, debt.Reason)
	return err
}

func listDebtors(chatID int64) ([]Debtor, error) {
	rows, err := DB.Query("SELECT id, name, payment_date, payment_amount FROM debtors WHERE chat_id = ?", chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var debtors []Debtor
	for rows.Next() {
		var debtor Debtor
		if err := rows.Scan(&debtor.ID, &debtor.Name, &debtor.PaymentDate, &debtor.PaymentAmount); err != nil {
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

func deleteDebtor(debtorID int) error {
	_, err := DB.Exec("DELETE FROM debtors WHERE id = ?", debtorID)
	return err
}

func updateDebtorPaymentDate(debtorID int, paymentDate time.Time) error {
	_, err := DB.Exec("UPDATE debtors SET payment_date = ? WHERE id = ?", paymentDate, debtorID)
	return err
}

func updateDebtorPaymentAmount(debtorID int, paymentAmount float64) error {
	_, err := DB.Exec("UPDATE debtors SET payment_amount = ? WHERE id = ?", paymentAmount, debtorID)
	return err
}

func clearDebtorPaymentDate(debtorID int) error {
	_, err := DB.Exec("UPDATE debtors SET payment_date = NULL WHERE id = ?", debtorID)
	return err
}

func clearDebtorPaymentAmount(debtorID int) error {
	_, err := DB.Exec("UPDATE debtors SET payment_amount = NULL WHERE id = ?", debtorID)
	return err
}

// --- CSV Export ---
func generateCSV(chatID int64) (string, error) {
	debtors, err := listDebtors(chatID)
	if err != nil {
		return "", err
	}

	if len(debtors) == 0 {
		return "", fmt.Errorf("no debtors found for chat %d", chatID)
	}

	tmpFile, err := os.CreateTemp("", "debts_*.csv")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	writer := csv.NewWriter(tmpFile)
	defer writer.Flush()

	header := []string{"Debtor Name", "Total Debt", "Payment Date", "Payment Amount", "Debt Reason", "Debt Amount"}
	if err := writer.Write(header); err != nil {
		return "", err
	}

	for _, debtor := range debtors {
		debts, err := listDebts(debtor.ID)
		if err != nil {
			return "", err
		}

		var totalDebt float64
		for _, debt := range debts {
			totalDebt += debt.Amount
		}

		paymentDateStr := ""
		if debtor.PaymentDate.Valid {
			paymentDateStr = debtor.PaymentDate.Time.Format("02.01.2006")
		}
		paymentAmountStr := ""
		if debtor.PaymentAmount.Valid {
			paymentAmountStr = fmt.Sprintf("%.2f", debtor.PaymentAmount.Float64)
		}

		if len(debts) > 0 {
			for _, debt := range debts {
				row := []string{
					debtor.Name,
					fmt.Sprintf("%.2f", totalDebt),
					paymentDateStr,
					paymentAmountStr,
					debt.Reason,
					fmt.Sprintf("%.2f", debt.Amount),
				}
				if err := writer.Write(row); err != nil {
					return "", err
				}
			}
		} else {
			row := []string{
				debtor.Name,
				fmt.Sprintf("%.2f", totalDebt),
				paymentDateStr,
				paymentAmountStr,
				"",
				"0.00",
			}
			if err := writer.Write(row); err != nil {
				return "", err
			}
		}
	}

	return tmpFile.Name(), nil

}

// --- Command Handlers ---

func handleStartCommand(bot *tgbotapi.BotAPI, chatID int64) {
	clearUserState(chatID)

	// Define the path to your image file
	imagePath := "botBanner.jpeg" //REPLACE

	// 1. Send the photo
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(imagePath))
	//   photo.Caption = "Welcome to DebtTracker!" // Optional caption
	_, err := bot.Send(photo)
	if err != nil {
		log.Printf("Error sending photo: %v", err)
		// Fallback to text-only, if the image fails.  Don't return; send the text.
		// You might want to send a message saying the image failed to load.
		sendSimpleMessage(bot, chatID, "Привет! Не удалось загрузить изображение, но я DebtTracker и я помогу тебе вести учет долгов.")
	}

	// 2. Send the text message (separately, for guaranteed delivery)
	text := "Привет! Я бот DebtTracker. Я помогу тебе вести учет долгов.\n\n" +
		"Основные команды:\n" +
		"/add - Добавить долг\n" +
		"/debts - Посмотреть список должников и долги\n" +
		"/exportcsv - Выгрузить данные в CSV\n" +
		"/help - Помощь и список команд"
	sendSimpleMessage(bot, chatID, text) // Use the existing function
}

func handleAddCommand(bot *tgbotapi.BotAPI, chatID int64) {
	clearUserState(chatID)
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
		debts, _ := listDebts(debtor.ID)
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
		"/add - Добавить новый долг. Бот спросит имя должника, причину и сумму.\n" +
		"/debts - Показать список всех твоих должников.  Можно выбрать должника, чтобы увидеть детализацию долгов, закрыть или отредактировать долги.\n" +
		"/exportcsv - Выгрузить данные в CSV файл.\n" +
		"/help - Показать это сообщение со списком команд."
	sendSimpleMessage(bot, chatID, text)
}

func handleExportCSVCommand(bot *tgbotapi.BotAPI, chatID int64) {
	clearUserState(chatID)
	filePath, err := generateCSV(chatID)
	if err != nil {
		log.Printf("Error generating CSV: %v", err)
		if strings.Contains(err.Error(), "no debtors found") {
			sendSimpleMessage(bot, chatID, "Нет данных для выгрузки. Сначала добавьте должников.")
		} else {
			sendSimpleMessage(bot, chatID, "Произошла ошибка при создании CSV файла.")
		}

		return
	}

	doc := tgbotapi.NewDocument(chatID, tgbotapi.FilePath(filePath))
	_, err = bot.Send(doc)
	if err != nil {
		log.Printf("Error sending CSV: %v", err)
		sendSimpleMessage(bot, chatID, "Произошла ошибка при отправке CSV файла.")
		return
	}

	err = os.Remove(filePath)
	if err != nil {
		log.Printf("Error deleting temp file: %v", err)
	}

}

// --- Message Handler ---

func handleMessage(bot *tgbotapi.BotAPI, update tgbotapi.Update) {
	chatID := update.Message.Chat.ID
	text := update.Message.Text
	state := userStates[chatID]

	switch state {
	case StateAddingDebtorName:
		debtor, err := getDebtorByName(text, chatID)
		if err != nil && err != sql.ErrNoRows {
			log.Printf("Error getting debtor: %v", err)
			sendSimpleMessage(bot, chatID, "Произошла ошибка при поиске должника.")
			clearUserState(chatID)
			return
		}

		if err == sql.ErrNoRows {
			newDebtor := Debtor{Name: text, ChatID: chatID}
			newDebtor, err = addDebtor(newDebtor)
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
			currentDebtors[chatID] = newDebtor
		} else {
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
		if err != nil || amount <= 0 {
			sendSimpleMessage(bot, chatID, "Пожалуйста, введи корректную сумму долга (положительное число).")
			return
		}

		debt := Debt{DebtorID: currentDebtors[chatID].ID, Amount: amount, Reason: selectedDebts[chatID].Reason}
		if err := addDebt(debt); err != nil {
			log.Printf("Error adding debt: %v", err)
			sendSimpleMessage(bot, chatID, "Произошла ошибка при добавлении долга.")
		} else {
			sendSimpleMessage(bot, chatID, fmt.Sprintf("✅ Долг добавлен! *%s* должен *%.2f ₽* за *%s*.", currentDebtors[chatID].Name, amount, debt.Reason))
		}
		clearUserState(chatID)

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
			showDebtorDetails(bot, chatID, currentDebtors[chatID].ID)
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

	case StateSubtractingFromDebt:
		amountToSubtract, err := strconv.ParseFloat(text, 64)
		if err != nil || amountToSubtract <= 0 {
			sendSimpleMessage(bot, chatID, "Пожалуйста, введи корректную сумму для вычитания (положительное число).")
			return
		}

		debt := selectedDebts[chatID]
		if amountToSubtract > debt.Amount {
			sendSimpleMessage(bot, chatID, "Сумма для вычитания не может быть больше суммы долга.")
			return
		}

		newAmount := debt.Amount - amountToSubtract
		if err := updateDebtAmount(debt.ID, newAmount); err != nil {
			log.Printf("Error subtracting from debt: %v", err)
			sendSimpleMessage(bot, chatID, "Не удалось вычесть сумму из долга.")
		} else {
			if newAmount == 0 {
				closeDebt(debt.ID)
				sendSimpleMessage(bot, chatID, fmt.Sprintf("✅ Долг в размере *%.2f ₽* за *%s* полностью погашен и закрыт.", debt.Amount, debt.Reason))

			} else {
				sendSimpleMessage(bot, chatID, fmt.Sprintf("Сумма *%.2f ₽* вычтена из долга.  Остаток долга: *%.2f ₽*", amountToSubtract, newAmount))

			}
			showDebtorDetails(bot, chatID, debt.DebtorID)
		}
		clearUserState(chatID)

	case StateSettingPaymentDate:
		var t time.Time
		var err error
		formats := []string{"02.01.2006", "02.01.06", "2.1.2006", "2.1.06", "02-01-2006", "02-01-06", "2-1-2006", "2-1-06"}
		for _, format := range formats {
			t, err = time.Parse(format, text)
			if err == nil {
				break
			}
		}

		if err != nil {
			sendSimpleMessage(bot, chatID, "Неверный формат даты. Пожалуйста, введите дату в формате ДД.ММ.ГГГГ или ДД.ММ.ГГ, например, 31.12.2024 или 31.12.24")
			return
		}
		currentDebtor := currentDebtors[chatID]
		err = updateDebtorPaymentDate(currentDebtor.ID, t)

		if err != nil {
			log.Printf("Error updating payment date: %v", err)
			sendSimpleMessage(bot, chatID, "Не удалось обновить дату платежа.")
		} else {
			sendSimpleMessage(bot, chatID, fmt.Sprintf("Дата платежа для %s установлена на %s", currentDebtor.Name, t.Format("02.01.2006")))
			showDebtorDetails(bot, chatID, currentDebtor.ID)
		}
		clearUserState(chatID)

	case StateSettingPaymentAmount:
		amount, err := strconv.ParseFloat(text, 64)
		if err != nil || amount <= 0 {
			sendSimpleMessage(bot, chatID, "Пожалуйста, введите корректную сумму платежа (положительное число).")
			return
		}
		currentDebtor := currentDebtors[chatID]

		if err := updateDebtorPaymentAmount(currentDebtor.ID, amount); err != nil {
			log.Printf("Error setting payment amount: %v", err)
			sendSimpleMessage(bot, chatID, "Не удалось установить сумму платежа.")
		} else {
			sendSimpleMessage(bot, chatID, fmt.Sprintf("Сумма платежа для *%s* установлена на *%.2f ₽*", currentDebtor.Name, amount))
		}
		clearUserState(chatID)
		showDebtorDetails(bot, chatID, currentDebtor.ID)

	case StateEditingPaymentDate:
		var t time.Time
		var err error
		formats := []string{"02.01.2006", "02.01.06", "2.1.2006", "2.1.06", "02-01-2006", "02-01-06", "2-1-2006", "2-1-06"}
		for _, format := range formats {
			t, err = time.Parse(format, text)
			if err == nil {
				break
			}
		}

		if err != nil {
			sendSimpleMessage(bot, chatID, "Неверный формат даты. Пожалуйста, введите дату в формате ДД.ММ.ГГГГ или ДД.ММ.ГГ")
			return
		}

		if err := updateDebtorPaymentDate(currentDebtors[chatID].ID, t); err != nil {
			log.Printf("Error updating payment date: %v", err)
			sendSimpleMessage(bot, chatID, "Не удалось обновить дату платежа.")
		} else {
			sendSimpleMessage(bot, chatID, fmt.Sprintf("Дата платежа обновлена на %s", t.Format("02.01.2006")))
			showDebtorDetails(bot, chatID, currentDebtors[chatID].ID)
		}
		clearUserState(chatID)

	case StateEditingPaymentAmount:
		amount, err := strconv.ParseFloat(text, 64)
		if err != nil || amount <= 0 {
			sendSimpleMessage(bot, chatID, "Пожалуйста, введите корректную сумму платежа (положительное число).")
			return
		}
		if err := updateDebtorPaymentAmount(currentDebtors[chatID].ID, amount); err != nil {
			log.Printf("Error updating payment amount: %v", err)
			sendSimpleMessage(bot, chatID, "Не удалось обновить сумму платежа.")
		} else {
			sendSimpleMessage(bot, chatID, "Сумма платежа успешно обновлена.")
		}
		clearUserState(chatID)
		showDebtorDetails(bot, chatID, currentDebtors[chatID].ID)

	default:
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

		debtor, err := getDebtorByID(debtorID)
		if err != nil {
			if err == sql.ErrNoRows {
				sendSimpleMessage(bot, chatID, "Должник не найден.")
			} else {
				log.Printf("Error getting debtor for details: %v", err)
				sendSimpleMessage(bot, chatID, "Произошла ошибка при получении информации о должнике.")
			}
			clearUserState(chatID)
			return
		}
		currentDebtors[chatID] = debtor
		clearUserState(chatID)
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
		selectedDebts[chatID] = debt
		userStates[chatID] = StateConfirmingCloseDebt
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ Да, закрыть", fmt.Sprintf("confirm_close:%d", debtID)),
				tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "cancel_operation"),
			),
		)
		editMessageWithKeyboard(bot, chatID, messageID, fmt.Sprintf("Вы уверены, что хотите закрыть долг *%.2f ₽* за *%s*?", debt.Amount, debt.Reason), keyboard)

	case strings.HasPrefix(data, "confirm_close:"):
		debtIDStr := strings.TrimPrefix(data, "confirm_close:")
		debtID, _ := strconv.Atoi(debtIDStr)
		if err := closeDebt(debtID); err != nil {
			log.Printf("Error closing debt in callback: %v", err)
			sendSimpleMessage(bot, chatID, "Произошла ошибка при закрытии долга.")
		} else {
			editMessageWithKeyboard(bot, chatID, messageID, "Долг закрыт.", tgbotapi.InlineKeyboardMarkup{})
		}
		showDebtorDetails(bot, chatID, currentDebtors[chatID].ID)
		clearUserState(chatID)

	case data == "cancel_operation":
		editMessageWithKeyboard(bot, chatID, messageID, "Операция отменена.", tgbotapi.InlineKeyboardMarkup{})
		clearUserState(chatID)
		if _, ok := currentDebtors[chatID]; ok {
			showDebtorDetails(bot, chatID, currentDebtors[chatID].ID)
		}

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
		selectedDebts[chatID] = debt
		userStates[chatID] = StateEditingChooseWhatToEdit

		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Изменить сумму", fmt.Sprintf("edit_amount:%d", debtID)),
				tgbotapi.NewInlineKeyboardButtonData("Изменить причину", fmt.Sprintf("edit_reason:%d", debtID)),
				tgbotapi.NewInlineKeyboardButtonData("Вычесть из долга", fmt.Sprintf("subtract_from_debt:%d", debtID)),
			),
		)
		editMessageWithKeyboard(bot, chatID, messageID, "Что ты хочешь изменить?", keyboard)

	case strings.HasPrefix(data, "edit_amount:"):
		debtIDStr := strings.TrimPrefix(data, "edit_amount:")
		debtID, _ := strconv.Atoi(debtIDStr)
		selectedDebts[chatID] = Debt{ID: debtID}
		userStates[chatID] = StateEditingAmount
		editMessageWithKeyboard(bot, chatID, messageID, "Введи новую сумму:", tgbotapi.InlineKeyboardMarkup{})

	case strings.HasPrefix(data, "edit_reason:"):
		debtIDStr := strings.TrimPrefix(data, "edit_reason:")
		debtID, _ := strconv.Atoi(debtIDStr)
		selectedDebts[chatID] = Debt{ID: debtID}
		userStates[chatID] = StateEditingReason
		editMessageWithKeyboard(bot, chatID, messageID, "Введи новую причину:", tgbotapi.InlineKeyboardMarkup{})

	case strings.HasPrefix(data, "subtract_from_debt:"):
		debtIDStr := strings.TrimPrefix(data, "subtract_from_debt:")
		debtID, err := strconv.Atoi(debtIDStr)
		if err != nil {
			log.Printf("Invalid debt ID in callback: %v", err)
			return
		}
		debt, err := getDebtByID(debtID)
		if err != nil {
			log.Printf("Error getting debt for subtraction: %v", err)
			return
		}
		selectedDebts[chatID] = debt
		userStates[chatID] = StateSubtractingFromDebt
		editMessageWithKeyboard(bot, chatID, messageID, fmt.Sprintf("Какую сумму вычесть из долга *%.2f ₽*?", debt.Amount), tgbotapi.InlineKeyboardMarkup{})

	case data == "add_debt_to_existing":
		userStates[chatID] = StateAddingDebtReason
		editMessageWithKeyboard(bot, chatID, messageID, fmt.Sprintf("Какова причина долга для *%s*?", currentDebtors[chatID].Name), tgbotapi.InlineKeyboardMarkup{})

	case data == "delete_debtor":
		userStates[chatID] = StateConfirmingDeleteDebtor
		keyboard := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Да, удалить", "confirm_delete_debtor"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "cancel_operation"),
		),
		)

		editMessageWithKeyboard(bot, chatID, messageID, fmt.Sprintf("Вы уверены, что хотите удалить должника *%s*?  *Все долги этого должника будут удалены!*", currentDebtors[chatID].Name), keyboard)

	case data == "confirm_delete_debtor":
		debtorID := currentDebtors[chatID].ID
		if err := deleteDebtor(debtorID); err != nil {
			log.Printf("Error deleting debtor: %v", err)
			sendSimpleMessage(bot, chatID, "Произошла ошибка при удалении должника.")

		} else {
			editMessageWithKeyboard(bot, chatID, messageID, fmt.Sprintf("Должник *%s* и все его долги удалены.", currentDebtors[chatID].Name), tgbotapi.InlineKeyboardMarkup{})
		}
		clearUserState(chatID)

	case data == "set_payment_date":
		userStates[chatID] = StateSettingPaymentDate
		editMessageWithKeyboard(bot, chatID, messageID, "Введите дату платежа (ДД.ММ.ГГГГ или ДД.ММ.ГГ):", tgbotapi.InlineKeyboardMarkup{})

	case data == "set_payment_amount":
		userStates[chatID] = StateSettingPaymentAmount
		editMessageWithKeyboard(bot, chatID, messageID, "Введите сумму платежа:", tgbotapi.InlineKeyboardMarkup{})

	case data == "clear_payment_date":
		if err := clearDebtorPaymentDate(currentDebtors[chatID].ID); err != nil {
			log.Printf("Error clearing payment date: %v", err)
			sendSimpleMessage(bot, chatID, "Не удалось очистить дату платежа.")
		} else {
			editMessageWithKeyboard(bot, chatID, messageID, "Дата платежа очищена.", tgbotapi.InlineKeyboardMarkup{})
			showDebtorDetails(bot, chatID, currentDebtors[chatID].ID)
		}
		clearUserState(chatID)

	case data == "clear_payment_amount":
		if err := clearDebtorPaymentAmount(currentDebtors[chatID].ID); err != nil {
			log.Printf("Error clearing payment amount: %v", err)
			sendSimpleMessage(bot, chatID, "Не удалось очистить сумму платежа.")
		} else {
			editMessageWithKeyboard(bot, chatID, messageID, "Сумма платежа очищена.", tgbotapi.InlineKeyboardMarkup{})
			showDebtorDetails(bot, chatID, currentDebtors[chatID].ID)
		}
		clearUserState(chatID)

	case data == "edit_payment_date":
		userStates[chatID] = StateEditingPaymentDate
		editMessageWithKeyboard(bot, chatID, messageID, "Введите новую дату платежа (ДД.ММ.ГГГГ или ДД.ММ.ГГ):", tgbotapi.InlineKeyboardMarkup{})

	case data == "edit_payment_amount":
		userStates[chatID] = StateEditingPaymentAmount
		editMessageWithKeyboard(bot, chatID, messageID, "Введите новую сумму платежа:", tgbotapi.InlineKeyboardMarkup{})
	}
}

// --- Show Debtor Details ---

func showDebtorDetails(bot *tgbotapi.BotAPI, chatID int64, debtorID int) {
	debtor, err := getDebtorByID(debtorID)
	if err != nil {
		log.Printf("Error getting debtor details: %v", err)
		if err == sql.ErrNoRows {
			sendSimpleMessage(bot, chatID, "Должник не найден.")
		} else {
			sendSimpleMessage(bot, chatID, "Произошла ошибка при получении информации о должнике.")
		}

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
		keyboardButtons = append(keyboardButtons, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✏️ Редактировать", fmt.Sprintf("edit_debt:%d", debt.ID)),
			tgbotapi.NewInlineKeyboardButtonData("✅ Закрыть", fmt.Sprintf("close_debt:%d", debt.ID)),
		))
	}

	debtsText.WriteString(fmt.Sprintf("\n*Общая сумма долга: %.2f ₽*", totalDebt))

	if debtor.PaymentDate.Valid {
		debtsText.WriteString(fmt.Sprintf("\n\n*Дата платежа:* %s", debtor.PaymentDate.Time.Format("02.01.2006")))
		keyboardButtons = append(keyboardButtons, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Изменить дату", "edit_payment_date"),
			tgbotapi.NewInlineKeyboardButtonData("Очистить дату", "clear_payment_date"),
		))
	} else {
		keyboardButtons = append(keyboardButtons, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Указать дату платежа", "set_payment_date"),
		))
	}

	if debtor.PaymentAmount.Valid {
		debtsText.WriteString(fmt.Sprintf("\n*Сумма платежа:* %.2f ₽", debtor.PaymentAmount.Float64))
		keyboardButtons = append(keyboardButtons, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Изменить сумму", "edit_payment_amount"),
			tgbotapi.NewInlineKeyboardButtonData("Очистить сумму", "clear_payment_amount"),
		))
	} else {
		keyboardButtons = append(keyboardButtons, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Указать сумму платежа", "set_payment_amount"),
		))
	}

	keyboardButtons = append(keyboardButtons, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("➕ Добавить долг", "add_debt_to_existing"),
		tgbotapi.NewInlineKeyboardButtonData("🗑️ Удалить должника", "delete_debtor"),
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

	bot.Debug = false

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
				case "exportcsv":
					handleExportCSVCommand(bot, update.Message.Chat.ID)
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
