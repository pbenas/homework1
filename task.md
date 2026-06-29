Coding task: Implement an HTTP service that is able to store objects organized by buckets. The service
should support the following methods: PUT, GET, DELETE. The objects are simply the text of an HTTP
request. They are identified by a URL path of the form "/objects/{bucket}/{objectID}".

Requirements:

- The service should be running on a port configured by the user. [i.e. `8080`].
- Objects can be stored in memory or on disk.
- The service should de-duplicate objects by buckets.
- You are free to use the language[s] of your choice and to make assumptions on definition.
- Organize your code sensibly, as if it's going into production
- Provide a repository on github.com with your solution.
- Please include a brief section (e.g., in your README or a separate document) detailing how, if at
  all, you utilized AI tools (e.g., ChatGPT, Gemini, GitHub Copilot, etc.) during your development
  process for this task. Be specific about what tasks AI assisted with (e.g., boilerplate generation,
  debugging, design pattern suggestions, etc.) and how you validated or iterated on its output.

API description:

- Upload object to the service
  - Request: PUT /objects/{bucket}/{objectID}
  - Response: Status: 201 Created { "id": "<objectID>" }
- Download an object from the service
  - Request: GET /objects/{bucket}/{objectID}
  - Response if object is found: Status: 200 OK {object data}
  - Response if object is not found: Status 404 Not Found
- Delete an object from the service
  - Request: DELETE /objects/{bucket}/{objectID}
  - Response if object found: Status: 200 OK
  - Response if object not found: Status: 404 Not Found
