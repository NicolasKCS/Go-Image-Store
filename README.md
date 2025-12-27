# Go-Image-Store

Important commands

1. docker compose up --build
2. docker compose exec app sh
3. echo "test1" > test1.png
4. curl -X POST http://localhost:8080/images -F "image=@test1.png"
5. curl -X DELETE http://localhost:8080/images/1